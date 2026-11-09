// Package agent provides the agent registry and the Turn/Step execution loop.
//
// It follows upstream packages/core/agent and agent-loop. Input enters the
// Inbox turn queue; a background worker processes one turn at a time:
// turn/start, claim, pre-step, one or more steps, turn-stopping, and turn/end.
// Each step derives state, emits agent/request, consumes llm/stream and
// assistant/chunk events, runs tools through the M23 pipeline, and closes.
// Step or tool errors emit agent/error and interrupt the turn. A full pending
// turn queue rejects Run and Followup with ErrTurnQueueFull.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sysprompt"
	"github.com/herlanai/omnix/pkg/tools"
)

// ErrTurnQueueFull indicates that the pending turn queue cannot accept Run or Followup.
var ErrDisposed = errors.New("agent: disposed")

var ErrTurnQueueFull = errors.New("agent: turn queue full, reject pending turn")

// CancelCause identifies one of the five cancellation categories (M18).
type CancelCause string

// Cancellation causes match the five upstream Agent.Cancel categories.
const (
	// CancelUser indicates an explicit user cancellation.
	CancelUser CancelCause = "user"
	// CancelParent indicates cancellation by a parent agent.
	CancelParent CancelCause = "parent"
	// CancelHook indicates cancellation after a hook rejects an operation.
	CancelHook CancelCause = "hook"
	// CancelDisposed indicates cancellation when the agent is disposed.
	CancelDisposed CancelCause = "disposed"
	// CancelLegacy identifies the legacy compatibility cancellation path.
	CancelLegacy CancelCause = "legacy"
)

// Agent executes agent turns and steps.
type Agent struct {
	// ID is the session ID shared with SessionLog.
	ID brand.SessionID
	// log is the event-sourced session log (M04).
	log *session.SessionLog
	// sys assembles system prompts (M09).
	sys *sysprompt.Assembler
	// pipeline is the four-stage tool pipeline (M23).
	pipeline *tools.Pipeline
	// adapter is the LLM adapter (M07).
	adapter llm.LLMAdapter
	// options configures the provider, model, reasoning effort, and token limit.
	options AgentOptions
	// inbox owns the next-turn and next-step pending input queues.
	inbox *Inbox

	// turnCh queues turn requests; capacity two accommodates one running and one pending turn.
	turnCh chan *turnReq
	// stopCh signals the worker to stop.
	stopCh chan struct{}
	// wg tracks the worker lifetime.
	wg sync.WaitGroup
	// started records whether the worker has started.
	started bool
	// mu protects started and status.
	mu sync.Mutex
	// status is the current lifecycle state: idle or running.
	status AgentStatus
	// idleCh notifies WhenIdle waiters when the agent becomes idle.
	idleCh chan struct{}
	// pendingToolRounds counts tool calls from the previous step to decide whether to continue.
	pendingToolRounds int
	// toolProvider resolves tools by name; tests may inject mocks and the harness supplies production tools.
	toolProvider func(name string) *tools.Tool
	// runCtx carries parent cancellation, deadlines, and tracing (H01); the default is Background.
	runCtx       context.Context
	runTimeout   time.Duration
	activeCancel context.CancelFunc
	generation   uint64
	disposed     bool
	pending      int
}

// turnReq describes a single turn request.
type turnReq struct {
	generation uint64
	// kind is run or followup.
	kind string
	// input is the user input.
	input string
	// done is a buffered, nonblocking completion notification.
	done chan error
}

// NewAgent creates an agent. A nil adapter may be replaced before Start in tests.
// Nil options select the defaults.
func NewAgent(id brand.SessionID, log *session.SessionLog, sys *sysprompt.Assembler, pipeline *tools.Pipeline, adapter llm.LLMAdapter, options ...AgentOptions) *Agent {
	opts := AgentOptions{}
	if len(options) > 0 {
		opts = options[0]
	}
	a := &Agent{
		ID:       id,
		log:      log,
		sys:      sys,
		pipeline: pipeline,
		adapter:  adapter,
		options:  opts,
		turnCh:   make(chan *turnReq, 2),
		stopCh:   make(chan struct{}),
		status:   StatusIdle,
		idleCh:   make(chan struct{}),
	}
	// Start idle and close idleCh so WhenIdle returns immediately.
	close(a.idleCh)
	// Initialize the inbox by replaying the session log.
	a.inbox = NewInbox(log, InboxNotifications{})
	return a
}

// Inbox returns the agent's pending next-turn and next-step input queues.
func (a *Agent) Inbox() *Inbox {
	return a.inbox
}

// Options returns the agent configuration for read-only use.
func (a *Agent) Options() AgentOptions {
	return a.options
}

// Status returns the current lifecycle state: idle or running.
func (a *Agent) Status() AgentStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// WhenIdle waits until agent activity settles; it returns immediately if already idle.
func (a *Agent) WhenIdle(ctx context.Context) error {
	a.mu.Lock()
	idleCh := a.idleCh
	a.mu.Unlock()

	select {
	case <-idleCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetToolProvider installs the tool resolver.
func (a *Agent) SetToolProvider(fn func(name string) *tools.Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolProvider = fn
}

// SetRunContext binds the parent context and an optional timeout budget (H01).
// A nonpositive timeout uses DefaultTimeout, or parent cancellation alone if both are zero.
func (a *Agent) SetRunContext(parent context.Context, timeout time.Duration) {
	if parent == nil {
		parent = context.Background()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.runCtx = parent
	a.runTimeout = timeout
}

// runContext returns the effective run context, falling back to Background rather than nil.
func (a *Agent) runContext() context.Context {
	if a.runCtx == nil {
		return context.Background()
	}
	return a.runCtx
}

// Log returns the event-sourced log for tests and higher-level projections.
func (a *Agent) Log() *session.SessionLog {
	return a.log
}

// loop serializes accepted turns.
func (a *Agent) loop() {
	defer a.wg.Done()
	for {
		select {
		case <-a.stopCh:
			return
		case req := <-a.turnCh:
			a.runTurn(req)
		}
	}
}

func (a *Agent) submit(input, kind string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.disposed {
		return ErrDisposed
	}
	if !a.started {
		a.started = true
		a.wg.Add(1)
		go a.loop()
	}
	req := &turnReq{generation: a.generation, kind: kind, input: input, done: make(chan error, 1)}
	select {
	case a.turnCh <- req:
		if a.pending == 0 {
			a.idleCh = make(chan struct{})
		}
		a.pending++
		return nil
	default:
		return ErrTurnQueueFull
	}
}

// Run queues a new turn. WhenIdle waits for all accepted work.
func (a *Agent) Run(input string) error { return a.submit(input, "run") }

// Followup queues a subsequent turn.
func (a *Agent) Followup(input string) error { return a.submit(input, "followup") }

// Dispose permanently closes the agent and cancels active and pending work.
// Tool and adapter implementations must honor context cancellation.
func (a *Agent) Dispose() {
	a.mu.Lock()
	if !a.disposed {
		a.disposed = true
		close(a.stopCh)
		if a.activeCancel != nil {
			_ = RecordCancel(a.log, CancelDisposed)
			a.activeCancel()
		}
		a.drainLocked()
	}
	a.mu.Unlock()
	a.wg.Wait()
}

func (a *Agent) drainLocked() {
	for {
		select {
		case req := <-a.turnCh:
			req.done <- context.Canceled
			a.pending--
			if a.pending == 0 {
				a.status = StatusIdle
				close(a.idleCh)
			}
		default:
			return
		}
	}
}

// Cancel aborts the active turn and discards queued work unless KeepInbox is set.
func (a *Agent) Cancel(cause CancelCause) { a.CancelWithOptions(cause, CancelOptions{}) }
func (a *Agent) CancelWithOptions(cause CancelCause, opts CancelOptions) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = RecordCancel(a.log, cause)
	if a.activeCancel != nil {
		a.activeCancel()
	}
	if !opts.KeepInbox {
		a.generation++
		a.drainLocked()
	}
}

// runTurn executes a turn; turns are serialized so only one runs at a time.
func (a *Agent) runTurn(req *turnReq) {
	var turnErr error
	a.mu.Lock()
	if a.disposed || req.generation != a.generation {
		a.pending--
		if a.pending == 0 {
			a.status = StatusIdle
			close(a.idleCh)
		}
		a.mu.Unlock()
		req.done <- context.Canceled
		return
	}
	parent := a.runContext()
	timeout := a.runTimeout
	if timeout <= 0 {
		timeout = a.options.DefaultTimeout
	}
	ctx, cancel := context.WithCancel(parent)
	if timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, timeout)
		oldCancel := cancel
		cancel = func() { timeoutCancel(); oldCancel() }
	}
	a.activeCancel = cancel
	if a.disposed {
		cancel()
	}
	wasIdle := a.status == StatusIdle
	a.status = StatusRunning
	a.mu.Unlock()
	if wasIdle {
		_, _ = a.log.Append(session.AgentStatusData{Status: string(StatusRunning)})
	}
	defer func() {
		cancel()
		a.mu.Lock()
		a.activeCancel = nil
		a.pending--
		if a.pending == 0 {
			a.status = StatusIdle
			_, _ = a.log.Append(session.AgentStatusData{Status: string(StatusIdle)})
			close(a.idleCh)
		}
		a.mu.Unlock()
		req.done <- turnErr
	}()

	// Read the current turn number from SessionLog to preserve strict monotonicity.
	turnIdx := a.log.NextTurn()

	// turn/start
	if _, err := a.log.Append(session.TurnStartData{Turn: turnIdx}); err != nil {
		turnErr = err
		return
	}

	// Record user/message for both Run and Followup input.
	if _, err := a.log.Append(session.UserMessageData{Content: req.input, Source: req.kind}); err != nil {
		_ = a.failTurn(turnIdx, turnErr, err)
		turnErr = err
		return
	}

	// Request the LLM in successive steps until no tools remain or an error occurs.
	stepSeq := uint64(1)
	for {
		// step/start
		if _, err := a.log.Append(session.StepStartData{Turn: turnIdx, Step: stepSeq}); err != nil {
			_ = a.failTurn(turnIdx, turnErr, err)
			turnErr = err
			return
		}

		err := a.runStep(ctx, req, stepSeq)
		// step/end
		_, _ = a.log.Append(session.StepEndData{Turn: turnIdx, Step: stepSeq})

		if err != nil {
			// Record agent/error and close the turn as interrupted.
			_ = a.failTurn(turnIdx, turnErr, err)
			turnErr = err
			return
		}

		// Continue only if runStep produced and executed tool calls.
		cont := a.lastStepHadToolCall()
		if !cont {
			break
		}
		stepSeq++
	}

	// turn-stopping → turn/end (finished)
	_, _ = a.log.Append(session.TurnStoppingData{Reason: "finished"})
	_, _ = a.log.Append(session.TurnEndData{Turn: turnIdx, Reason: session.ReasonFinished})
}

// lastStepHadToolCall reports whether the previous step executed a tool call.
func (a *Agent) lastStepHadToolCall() bool {
	return a.pendingToolRounds > 0
}

// failTurn records agent/error and closes the turn as interrupted.
func (a *Agent) failTurn(turnIdx uint64, prev, err error) error {
	_, _ = a.log.Append(session.AgentErrorData{Message: err.Error(), Pkg: "pkg/agent"})
	_, _ = a.log.Append(session.TurnEndData{Turn: turnIdx, Reason: endReason(err)})
	return err
}

// runStep derives state, assembles a request, consumes the stream, and executes tools.
func (a *Agent) runStep(runCtx context.Context, req *turnReq, stepSeq uint64) error {
	if err := runCtx.Err(); err != nil {
		return err
	}
	if a.adapter == nil {
		return errors.New("agent: LLM adapter missing")
	}
	// agent/request
	_, _ = a.log.Append(session.AgentRequestData{Provider: a.adapterName(), Model: a.modelName()})

	// Assemble the prompt.
	system := ""
	if a.sys != nil {
		system = a.sys.Assemble()
	}
	schemas, schemaErr := a.toolSchemas()
	if schemaErr != nil {
		return schemaErr
	}
	messages := deriveModelMessages(a.log.Events())
	var toolCalls []*llm.ToolCall
	var text, reasoning strings.Builder
	_, err := a.adapter.Chat(runCtx, llm.ChatRequest{
		Model:           a.modelName(),
		System:          system,
		Messages:        messages,
		Tools:           schemas,
		MaxTokens:       a.options.MaxTokens,
		ReasoningEffort: a.options.ReasoningEffort,
	}, func(chunk llm.StreamChunk) {
		switch chunk.Kind {
		case llm.ChunkText:
			text.WriteString(chunk.Text)
			_, _ = a.log.Append(session.AssistantChunkData{Text: chunk.Text})
		case llm.ChunkReasoning:
			reasoning.WriteString(chunk.Reasoning)
			_, _ = a.log.Append(session.AssistantReasoningData{Text: chunk.Reasoning})
		case llm.ChunkToolCall:
			if chunk.ToolCall != nil {
				toolCalls = append(toolCalls, chunk.ToolCall)
			}
		}
	})
	if text.Len() > 0 || reasoning.Len() > 0 || len(toolCalls) > 0 {
		ids := make([]string, 0, len(toolCalls))
		if err == nil {
			for _, tc := range toolCalls {
				ids = append(ids, tc.ID)
			}
		}
		if _, appendErr := a.log.Append(session.AssistantMessageData{Content: text.String(), Reasoning: reasoning.String(), ToolCallIDs: ids}); appendErr != nil {
			return appendErr
		}
	}
	if err != nil {
		return fmt.Errorf("llm step failed: %w", err)
	}
	if err := runCtx.Err(); err != nil {
		return err
	}

	// Execute tool calls.
	if len(toolCalls) > 0 {
		a.pendingToolRounds = len(toolCalls)
		for _, tc := range toolCalls {
			if err := a.executeTool(runCtx, tc); err != nil {
				return err
			}
		}
		a.pendingToolRounds = len(toolCalls)
	} else {
		a.pendingToolRounds = 0
	}
	return nil
}

// pendingToolRounds counts the previous step's tool calls.
// A nonzero count continues the loop, matching the upstream ReAct loop.

// executeTool runs one tool call through the M23 pipeline.
func (a *Agent) executeTool(ctx context.Context, tc *llm.ToolCall) error {
	callID := brand.NewToolCallID(tc.ID)
	// tool/call
	if _, err := a.log.Append(session.ToolCallData{CallID: callID, Tool: tc.Name, Input: toRaw(tc.Input)}); err != nil {
		return err
	}

	if a.pipeline == nil {
		// Record the result directly when no pipeline is configured.
		_, _ = a.log.Append(session.ToolResultData{CallID: callID, IsError: true, Output: "tool pipeline missing"})
		return nil
	}

	req := &tools.ToolCallRequest{CallID: callID, Tool: tc.Name, Input: tc.Input}
	res := a.pipeline.Run(ctx, req, a.findTool(tc.Name))
	output := stringify(res.Value)
	if res.IsError && res.Error != "" {
		output = res.Error
	}
	_, err := a.log.Append(session.ToolResultData{
		CallID: callID, IsError: res.IsError, Output: output,
	})
	return err
}

// adapterName / modelName resolve adapter and configured model information.
func (a *Agent) adapterName() string {
	if a.adapter == nil {
		return "unknown"
	}
	return a.adapter.Name()
}

func (a *Agent) modelName() string {
	if a.options.Model != "" {
		return a.options.Model
	}
	return "deepseek-chat"
}

// toolSchemas exports the registered tool definitions in deterministic order.
func (a *Agent) toolSchemas() ([]llm.ToolSchema, error) {
	if a.pipeline == nil {
		return nil, nil
	}
	var out []llm.ToolSchema
	for _, tool := range a.pipeline.Tools() {
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if tool.Schema != nil {
			data, err := json.Marshal(tool.Schema)
			if err != nil {
				return nil, fmt.Errorf("agent: tool %q schema: %w", tool.Name, err)
			}
			if err := json.Unmarshal(data, &params); err != nil {
				return nil, err
			}
		}
		out = append(out, llm.ToolSchema{Name: tool.Name, Description: tool.Description, Parameters: params})
	}
	return out, nil
}

// findTool resolves a tool through the injected resolver, or returns nil if none is installed.
func (a *Agent) findTool(name string) *tools.Tool {
	a.mu.Lock()
	provider := a.toolProvider
	a.mu.Unlock()
	if provider != nil {
		return provider(name)
	}
	if a.pipeline != nil {
		return a.pipeline.Lookup(name)
	}
	return nil
}

// jsonMarshal is a JSON serialization helper.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// toRaw converts map arguments into json.RawMessage.
func toRaw(input map[string]any) []byte {
	if input == nil {
		return nil
	}
	b, _ := jsonMarshal(input)
	return b
}

// stringify converts a tool result to a string.
func stringify(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := jsonMarshal(v)
	return string(b)
}

func endReason(err error) session.TurnEndReason {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return session.ReasonAborted
	}
	return session.ReasonInterrupted
}
