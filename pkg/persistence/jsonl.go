// Package persistence provides checkpoint flushing, batched writes, and crash
// repair, following upstream session persistence and JSONL storage. Persistence
// abstracts locate/load/append/list/snapshot operations. JSONLBackend appends
// events and supports checkpoints. H02 shards session IDs across independent
// locks and queues; background writers flush at size or time thresholds to
// reduce file operations. Crash repair closes unmatched turns as interrupted
// while preserving persisted chunks and tool events.
package persistence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// H05 reuses bytes.Buffer and bufio.Writer objects through sync.Pool.
// Marshal buffers start at 4 KiB and retain grown backing storage after Reset.
// Buffered writers also use 4 KiB and reset onto each destination file.
// Atomic counters expose reuse, serialized bytes, and pooled activity for
// benchmarks and telemetry.

// defaultMarshalBufCap is 4 KiB, accommodating typical sub-kilobyte session events.
const defaultMarshalBufCap = 4 * 1024

// defaultBufioWriterSize matches the standard 4 KiB buffered writer size.
const defaultBufioWriterSize = 32 * 1024

var (
	// marshalBufPool supplies reusable JSON encoding buffers (H05).
	marshalBufPool = sync.Pool{
		New: func() any { return bytes.NewBuffer(make([]byte, 0, defaultMarshalBufCap)) },
	}
	// bufioWriterPool reuses writers by resetting their output destination (H05).
	bufioWriterPool = sync.Pool{
		New: func() any { return bufio.NewWriterSize(io.Discard, defaultBufioWriterSize) },
	}
)

// H05 atomic statistics accumulate across all JSONLBackend instances.
var (
	ioStatPooledBufferHits     atomic.Uint64 // Combined buffer and writer pool acquisitions.
	ioStatMarshaledBytes       atomic.Uint64 // Total bytes written through pooled encoding.
	ioStatMarshaledEvents      atomic.Uint64 // Events serialized through the pooled path.
	ioStatHeaderMarshaledBytes atomic.Uint64 // Total serialized header bytes.
)

// JSONLIOStats is a thread-safe snapshot of H05 performance counters.
type JSONLIOStats struct {
	PooledBufferHits     uint64 `json:"pooledBufferHits"`
	MarshaledBytes       uint64 `json:"marshaledBytes"`
	MarshaledEvents      uint64 `json:"marshaledEvents"`
	HeaderMarshaledBytes uint64 `json:"headerMarshaledBytes"`
}

// ReadJSONLIOStats returns the current H05 counters.
func ReadJSONLIOStats() JSONLIOStats {
	return JSONLIOStats{
		PooledBufferHits:     ioStatPooledBufferHits.Load(),
		MarshaledBytes:       ioStatMarshaledBytes.Load(),
		MarshaledEvents:      ioStatMarshaledEvents.Load(),
		HeaderMarshaledBytes: ioStatHeaderMarshaledBytes.Load(),
	}
}

// ResetJSONLIOStats resets counters for tests and benchmarks, not normal production use.
func ResetJSONLIOStats() {
	ioStatPooledBufferHits.Store(0)
	ioStatMarshaledBytes.Store(0)
	ioStatMarshaledEvents.Store(0)
	ioStatHeaderMarshaledBytes.Store(0)
}

// pooledMarshalEvent encodes v into a pooled buffer with exactly one trailing
// newline. Call releasePooledBuffer after using the returned bytes; they remain
// valid only until the buffer is reset.
func pooledMarshalEvent(v any) (buf *bytes.Buffer, err error) {
	buf, _ = marshalBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	ioStatPooledBufferHits.Add(1)
	enc := json.NewEncoder(buf)
	if err = enc.Encode(v); err != nil {
		return
	}
	return
}

// releasePooledBuffer returns a buffer to the pool.
func releasePooledBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	marshalBufPool.Put(buf)
}

// pooledBufioWriter acquires a writer and resets it onto the destination.
// Call releasePooledWriter after use; it flushes before returning the writer.
func pooledBufioWriter(w io.Writer) *bufio.Writer {
	bw, _ := bufioWriterPool.Get().(*bufio.Writer)
	bw.Reset(w)
	ioStatPooledBufferHits.Add(1)
	return bw
}

// releasePooledWriter flushes and returns the writer to the pool.
func releasePooledWriter(bw *bufio.Writer) error {
	if bw == nil {
		return nil
	}
	err := bw.Flush()
	// Drop the external destination reference before returning the writer to the pool.
	bw.Reset(io.Discard)
	bufioWriterPool.Put(bw)
	return err
}

// Defaults for H02 sharding and asynchronous batching.

const (
	// DefaultShardCount is a power of two for efficient indexing.
	// WithShardCount can increase it for workloads with many concurrent sessions.
	DefaultShardCount = 16
	// DefaultFlushInterval sets the asynchronous flush window in milliseconds.
	// Each tick flushes all buffered sessions in the shard.
	DefaultFlushInterval = 100 * time.Millisecond
)

// Persistence interface.

// Persistence abstracts session persistence backends.
type Persistence interface {
	// Locate returns the persistent session path.
	Locate(ctx context.Context, id brand.SessionID) (string, error)
	// Load reads the session header and all events.
	Load(ctx context.Context, id brand.SessionID) (*session.SessionHeader, []session.SessionEvent, error)
	// Append buffers an event for the batch window.
	Append(ctx context.Context, id brand.SessionID, ev session.SessionEvent) error
	// Flush writes buffered events to disk as a checkpoint.
	Flush(ctx context.Context, id brand.SessionID) error
	// Snapshot returns the current event count for a session.
	Snapshot(ctx context.Context, id brand.SessionID) (int, error)
	// List returns all persisted session IDs.
	List(ctx context.Context) ([]brand.SessionID, error)
}

// JSONLBackend uses sharded locks and asynchronous batched writes (H02).

// shardBuf owns a mutex, per-session event buffers, and a file-write lock.
// Different shards avoid Append/Flush contention. Within a shard, writeMu
// serializes appendEventsToFile so writers cannot interleave on one session file.
type shardBuf struct {
	mu  sync.Mutex
	buf map[brand.SessionID][]session.SessionEvent
	// writeMu serializes file writes from flushShard, Flush, and FlushAll within
	// this shard. Different shards can write concurrently.
	writeMu sync.Mutex
}

// JSONLBackend persists events in JSONL files. dir is the storage root;
// batchSize and flushInterval trigger flushing by count or time. Session hashes
// select a fixed shard using shardMask, requiring a power-of-two shard count.
// flushCh signals shard writers; closeOnce/closeCh coordinate shutdown, and
// wg lets Close wait until all background writers exit.
type JSONLBackend struct {
	dir           string
	batchSize     int
	flushInterval time.Duration

	shards    []*shardBuf
	shardMask uint32

	// flushCh triggers immediate shard flushing when Append reaches its threshold.
	flushChs []chan struct{}

	closeOnce sync.Once
	closeCh   chan struct{}
	wg        sync.WaitGroup
}

// Option configures JSONLBackend through functional options.
type Option func(*jsonlOptions)

type jsonlOptions struct {
	shardCount    int
	flushInterval time.Duration
}

// WithShardCount sets the shard count, rounding up to a power of two.
func WithShardCount(n int) Option {
	return func(o *jsonlOptions) { o.shardCount = n }
}

// WithFlushInterval sets the asynchronous flush window.
func WithFlushInterval(d time.Duration) Option {
	return func(o *jsonlOptions) { o.flushInterval = d }
}

// NewJSONL creates the backend and its directory if needed. A session flushes
// when its buffered count reaches batchSize or the time window expires.
// Nonpositive batchSize defaults to one. Options override DefaultShardCount
// and DefaultFlushInterval.
func NewJSONL(dir string, batchSize int, opts ...Option) (*JSONLBackend, error) {
	// Apply options.
	o := &jsonlOptions{
		shardCount:    DefaultShardCount,
		flushInterval: DefaultFlushInterval,
	}
	for _, f := range opts {
		f(o)
	}
	// Round the shard count up to a power of two for mask-based indexing.
	shardCount := nextPow2(o.shardCount)
	if shardCount < 1 {
		shardCount = 1
	}
	if batchSize <= 0 {
		batchSize = 1
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// Create shards and their trigger channels.
	shards := make([]*shardBuf, shardCount)
	flushChs := make([]chan struct{}, shardCount)
	for i := 0; i < shardCount; i++ {
		shards[i] = &shardBuf{buf: map[brand.SessionID][]session.SessionEvent{}}
		flushChs[i] = make(chan struct{}, 1) // Buffer one signal to coalesce repeated flush requests.
	}
	jb := &JSONLBackend{
		dir:           dir,
		batchSize:     batchSize,
		flushInterval: o.flushInterval,
		shards:        shards,
		shardMask:     uint32(shardCount - 1),
		flushChs:      flushChs,
		closeCh:       make(chan struct{}),
	}
	// Start one background writer per shard (H02).
	for i := 0; i < shardCount; i++ {
		jb.wg.Add(1)
		go jb.shardWriter(i)
	}
	return jb, nil
}

// nextPow2 returns the smallest power of two at least n, or one for n <= 1.
func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// shardIndex hashes SessionID with FNV-1a and masks the result.
// The mask requires a power-of-two shard count.
func (j *JSONLBackend) shardIndex(id brand.SessionID) int {
	h := fnv.New32a()
	// The immutable ID string gives a stable hash.
	_, _ = h.Write([]byte(id.Raw())) // FNV writes do not fail.
	return int(h.Sum32() & j.shardMask)
}

// shardWriter flushes nonempty buffers on timer ticks or threshold signals.
// When closeCh closes, it performs a final flush before exiting.
func (j *JSONLBackend) shardWriter(idx int) {
	defer j.wg.Done()
	sh := j.shards[idx]
	trigger := j.flushChs[idx]
	ticker := time.NewTicker(j.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-j.closeCh:
			// Flush remaining buffers during graceful shutdown.
			_ = j.flushShard(sh)
			return
		case <-trigger:
			_ = j.flushShard(sh)
		case <-ticker.C:
			_ = j.flushShard(sh)
		}
	}
}

// flushShard appends each session's buffered events to its JSONL file.
// writeMu covers buffer handoff and file writing, preserving checkpoint order.
func (j *JSONLBackend) flushShard(sh *shardBuf) error {
	sh.writeMu.Lock()
	defer sh.writeMu.Unlock()
	// Take ownership of all buffers while holding the lock.
	sh.mu.Lock()
	if len(sh.buf) == 0 {
		sh.mu.Unlock()
		return nil
	}
	snapshot := sh.buf
	sh.buf = make(map[brand.SessionID][]session.SessionEvent, len(snapshot))
	sh.mu.Unlock()

	// 2. Hold writeMu through writes; Append may continue under the independent buffer mutex.
	for id, events := range snapshot {
		if len(events) == 0 {
			continue
		}
		if err := j.appendEventsToFile(id, events); err != nil {
			// Restore events to the buffer on failure so a later flush can retry.
			sh.mu.Lock()
			for pendingID, pendingEvents := range snapshot {
				sh.buf[pendingID] = append(pendingEvents, sh.buf[pendingID]...)
			}
			sh.mu.Unlock()
			return err
		}
		delete(snapshot, id)
	}
	return nil
}

// appendEventsToFile appends a batch to a session's JSONL file. The caller must
// hold the shard's writeMu to serialize same-shard writes; other shards may
// write concurrently. H05 reuses encoding buffers and buffered writers.
// Encoder-provided newlines avoid an extra allocation per event.
func (j *JSONLBackend) appendEventsToFile(id brand.SessionID, events []session.SessionEvent) error {
	dir := j.sessionDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(j.dataPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := pooledBufioWriter(f)
	defer func() { _ = releasePooledWriter(w) }()
	for _, ev := range events {
		buf, merr := pooledMarshalEvent(ev)
		if merr != nil {
			return merr
		}
		n, werr := w.Write(buf.Bytes())
		ioStatMarshaledBytes.Add(uint64(n))
		ioStatMarshaledEvents.Add(1)
		releasePooledBuffer(buf)
		if werr != nil {
			return werr
		}
	}
	// The deferred releasePooledWriter also flushes.
	return nil
}

// Close stops background writers, flushes remaining buffers, and waits for exit.
// Repeated calls are safe. Append after Close is unsupported.
func (j *JSONLBackend) Close() error {
	j.closeOnce.Do(func() {
		close(j.closeCh)
	})
	j.wg.Wait()
	return nil
}

// ShardCount returns the actual shard count for tests and monitoring.
func (j *JSONLBackend) ShardCount() int { return len(j.shards) }

// Path helpers.

// sessionDir returns the session's storage directory.
func (j *JSONLBackend) sessionDir(id brand.SessionID) string {
	return filepath.Join(j.dir, id.Raw())
}

// headerPath and dataPath locate the session header and event file.
func (j *JSONLBackend) headerPath(id brand.SessionID) string {
	return filepath.Join(j.sessionDir(id), "header.json")
}
func (j *JSONLBackend) dataPath(id brand.SessionID) string {
	return filepath.Join(j.sessionDir(id), "events.jsonl")
}

// Persistence interface implementation.

// Locate implements Persistence.
func (j *JSONLBackend) Locate(ctx context.Context, id brand.SessionID) (string, error) {
	return j.sessionDir(id), nil
}

// SaveHeader writes or replaces the session header.
func (j *JSONLBackend) SaveHeader(ctx context.Context, h *session.SessionHeader) error {
	dir := j.sessionDir(h.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := h.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(j.headerPath(h.ID), data, 0o644)
}

// Append buffers an event in its session's shard and signals an immediate
// flush at batchSize. Disk writes run asynchronously in the shard writer.
func (j *JSONLBackend) Append(ctx context.Context, id brand.SessionID, ev session.SessionEvent) error {
	idx := j.shardIndex(id)
	sh := j.shards[idx]

	sh.mu.Lock()
	sh.buf[id] = append(sh.buf[id], ev)
	need := len(sh.buf[id]) >= j.batchSize
	sh.mu.Unlock()

	if need {
		// Send without blocking. A buffered signal already guarantees an upcoming flush.
		select {
		case j.flushChs[idx] <- struct{}{}:
		default:
		}
	}
	return nil
}

// Flush synchronously persists a session's buffered events. It takes ownership
// of the buffer and writes under writeMu, serialized with background flushing.
// Callers typically use it at turn/end checkpoints for durability.
func (j *JSONLBackend) Flush(ctx context.Context, id brand.SessionID) error {
	idx := j.shardIndex(id)
	sh := j.shards[idx]

	sh.writeMu.Lock()
	defer sh.writeMu.Unlock()
	// Take the session buffer; a concurrently awakened writer will see it empty.
	sh.mu.Lock()
	events := sh.buf[id]
	delete(sh.buf, id)
	sh.mu.Unlock()

	if len(events) == 0 {
		return nil
	}
	if err := j.appendEventsToFile(id, events); err != nil {
		sh.mu.Lock()
		sh.buf[id] = append(events, sh.buf[id]...)
		sh.mu.Unlock()
		return err
	}
	return nil
}

// FlushAll synchronously flushes each shard in order, typically before exit or in tests.
func (j *JSONLBackend) FlushAll(ctx context.Context) error {
	for _, sh := range j.shards {
		if err := j.flushShard(sh); err != nil {
			return err
		}
	}
	return nil
}

// Load reads the header and events and repairs interrupted sessions.
// It reads disk rather than pending buffers; call Flush or FlushAll first to
// observe recent appends. Historical reads need no preliminary flush.
func (j *JSONLBackend) Load(ctx context.Context, id brand.SessionID) (*session.SessionHeader, []session.SessionEvent, error) {
	// Read the header.
	hData, err := os.ReadFile(j.headerPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("persistence: session %q not found", id.Raw())
		}
		return nil, nil, err
	}
	header, err := session.UnmarshalSessionHeader(hData)
	if err != nil {
		return nil, nil, err
	}

	// Read event lines.
	f, err := os.Open(j.dataPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return header, nil, nil
		}
		return nil, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var events []session.SessionEvent
	for scanner.Scan() {
		var ev session.SessionEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			return nil, nil, fmt.Errorf("persistence: corrupt line: %w", err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	// Repair an unclosed final turn by appending an interrupted ending.
	if repaired := repairOrphanTurn(&events); repaired > 0 {
		if err := j.rewrite(header, events); err != nil {
			return nil, nil, err
		}
	}
	return header, events, nil
}

// rewrite replaces session files after crash repair. Events use pooled
// encoding buffers and writers; headers retain Header.Marshal's byte format
// for compatibility with Load and use a pooled temporary buffer.
func (j *JSONLBackend) rewrite(header *session.SessionHeader, events []session.SessionEvent) error {
	dir := j.sessionDir(header.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Preserve Header.Marshal output so escaping or newline differences do not
	// change hashes on reload. Use a pooled buffer as temporary storage.
	hData, err := header.Marshal()
	if err != nil {
		return err
	}
	hBuf, _ := marshalBufPool.Get().(*bytes.Buffer)
	hBuf.Reset()
	ioStatPooledBufferHits.Add(1)
	_, _ = hBuf.Write(hData)
	// Copy header bytes before releasing the pooled buffer so subsequent reuse
	// cannot change the data passed to WriteFile.
	hBytes := append([]byte(nil), hBuf.Bytes()...)
	releasePooledBuffer(hBuf)
	ioStatHeaderMarshaledBytes.Add(uint64(len(hBytes)))
	if err := os.WriteFile(j.headerPath(header.ID), hBytes, 0o644); err != nil {
		return err
	}
	// Replace event data using pooled writers and encoding buffers.
	f, err := os.Create(j.dataPath(header.ID))
	if err != nil {
		return err
	}
	defer f.Close()
	w := pooledBufioWriter(f)
	defer func() { _ = releasePooledWriter(w) }()
	for _, ev := range events {
		buf, merr := pooledMarshalEvent(ev)
		if merr != nil {
			return merr
		}
		n, werr := w.Write(buf.Bytes())
		ioStatMarshaledBytes.Add(uint64(n))
		ioStatMarshaledEvents.Add(1)
		releasePooledBuffer(buf)
		if werr != nil {
			return werr
		}
	}
	return nil
}

// Snapshot returns the complete event count after synchronously flushing the session.
func (j *JSONLBackend) Snapshot(ctx context.Context, id brand.SessionID) (int, error) {
	if err := j.Flush(ctx, id); err != nil {
		return 0, err
	}
	_, events, err := j.Load(ctx, id)
	if err != nil {
		return 0, err
	}
	return len(events), nil
}

// List returns all session directories.
func (j *JSONLBackend) List(ctx context.Context) ([]brand.SessionID, error) {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, err
	}
	var out []brand.SessionID
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, brand.NewSessionID(e.Name()))
		}
	}
	return out, nil
}

// Crash repair.

// repairOrphanTurn closes an unfinished turn by appending error results for
// pending tools, any missing step/end, and an interrupted turn/end.
// It returns the number of appended events.
func repairOrphanTurn(events *[]session.SessionEvent) int {
	if len(*events) == 0 {
		return 0
	}
	turnOpen := false
	stepOpen := false
	var openTurn uint64
	var openStep uint64
	// pending retains unresolved tool-call IDs in arrival order.
	var pending []string
	hasResult := map[string]bool{}
	for _, ev := range *events {
		switch ev.Type {
		case session.EventTurnStart:
			turnOpen, stepOpen = true, false
			pending = nil
			if td, ok := ev.Data.(session.TurnStartData); ok {
				openTurn = td.Turn
			}
		case session.EventTurnEnd:
			turnOpen, stepOpen = false, false
			pending = nil
		case session.EventStepStart:
			stepOpen = true
			if sd, ok := ev.Data.(session.StepStartData); ok {
				openStep = sd.Step
			}
		case session.EventStepEnd:
			stepOpen = false
		case session.EventAssistantMessage:
			if am, ok := ev.Data.(session.AssistantMessageData); ok {
				for _, id := range am.ToolCallIDs {
					if !hasResult[id] {
						pending = append(pending, id)
					}
				}
			}
		case session.EventToolCall:
			if call, ok := ev.Data.(session.ToolCallData); ok {
				pending = append(pending, call.CallID.Raw())
			}
		case session.EventToolResult:
			if tr, ok := ev.Data.(session.ToolResultData); ok {
				hasResult[tr.CallID.Raw()] = true
			}
		}
	}
	if !turnOpen {
		return 0
	}
	last := (*events)[len(*events)-1]
	seq := last.Seq
	time := last.Time
	add := func(ev session.SessionEvent) {
		seq++
		ev.Seq, ev.Time = seq, time
		*events = append(*events, ev)
	}
	n := 0
	// Append error results for unresolved calls.
	for _, id := range pending {
		if hasResult[id] {
			continue
		}
		add(session.SessionEvent{
			Type: session.EventToolResult,
			Data: session.ToolResultData{
				CallID:  brand.NewToolCallID(id),
				IsError: true,
				Output:  "工具调用在记录结果前被中断，结果未知；如仍需要请在确认外部状态后重试。",
			},
		})
		hasResult[id] = true
		n++
	}
	// Close an open step.
	if stepOpen {
		add(session.SessionEvent{
			Type: session.EventStepEnd,
			Data: session.StepEndData{Turn: openTurn, Step: openStep},
		})
		n++
	}
	// Close the turn as interrupted.
	add(session.SessionEvent{
		Type: session.EventTurnEnd,
		Data: session.TurnEndData{Turn: openTurn, Reason: session.ReasonInterrupted},
	})
	n++
	return n
}
