package persistence

import (
	"context"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFlushWaitsForInflightWriter(t *testing.T) {
	b, err := NewJSONL(t.TempDir(), 100, WithFlushInterval(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	id := brand.NewSessionID("flush")
	sh := b.shards[b.shardIndex(id)]
	sh.writeMu.Lock()
	done := make(chan error, 1)
	go func() { done <- b.Flush(context.Background(), id) }()
	select {
	case err := <-done:
		sh.writeMu.Unlock()
		t.Fatalf("Flush returned before writer completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	sh.writeMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Flush remained blocked")
	}
}

func TestFlushFailureRestoresEveryUnwrittenSession(t *testing.T) {
	dir := t.TempDir()
	b, err := NewJSONL(dir, 100, WithShardCount(1), WithFlushInterval(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ids := []brand.SessionID{brand.NewSessionID("a"), brand.NewSessionID("b")}
	for _, id := range ids {
		// A file where a directory is needed forces every write to fail before bytes are written.
		if err := os.WriteFile(filepath.Join(dir, id.Raw()), []byte("blocked"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := b.Append(context.Background(), id, session.SessionEvent{Seq: 1, Type: session.EventUserMessage, Data: session.UserMessageData{Content: id.Raw()}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.FlushAll(context.Background()); err == nil {
		t.Fatal("expected write failure")
	}
	sh := b.shards[0]
	sh.mu.Lock()
	for _, id := range ids {
		if len(sh.buf[id]) != 1 {
			t.Errorf("lost unwritten session %s", id.Raw())
		}
	}
	sh.mu.Unlock()
	for _, id := range ids {
		if err := os.Remove(filepath.Join(dir, id.Raw())); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if data, err := os.ReadFile(b.dataPath(id)); err != nil || len(data) == 0 {
			t.Fatalf("retry %s: %s %v", id.Raw(), data, err)
		}
	}
}
