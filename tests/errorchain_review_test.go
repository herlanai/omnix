// R05 errorChain compatibility tests cover colon-separated causes, exact duplicate suppression,
// errors.Join members rendered as [a; b], non-error message extraction or string conversion,
// circular-cause detection, and type-name fallback for empty messages. Upstream:
// packages/llm/llm/src/error.ts.
package tests

import (
	"errors"
	"fmt"
	"testing"

	"github.com/herlanai/omnix/pkg/llm"
)

// simpleErr tests type-name fallback for an empty error message.
type simpleErr struct{}

func (simpleErr) Error() string { return "" }

// llmFailureMessage has the same message as its cause.
type llmFailureMessage struct{ msg string }

func (e *llmFailureMessage) Error() string { return e.msg }
func (e *llmFailureMessage) Unwrap() error { return errors.New(e.msg) } // Identical cause text.

// circularA and circularB wrap each other to create a cause cycle.
type circularA struct{ next error }

func (circularA) Error() string   { return "A" }
func (c circularA) Unwrap() error { return c.next }

type circularB struct{ next error }

func (circularB) Error() string   { return "B" }
func (c circularB) Unwrap() error { return c.next }

func TestR05SingleCauseChain(t *testing.T) {
	base := errors.New("base failure")
	wrapped := fmt.Errorf("wrap: %w", base)
	got := llm.RenderErrorChain(wrapped)
	// Suppress only exact duplicate messages. A cause that is merely a substring must still be
	// appended as ': base failure'.
	if got != "wrap: base failure: base failure" {
		t.Fatalf("single cause chain = %q, want %q", got, "wrap: base failure: base failure")
	}
}

func TestR05RepeatedCauseSkipped(t *testing.T) {
	e := &llmFailureMessage{msg: "dup"}
	got := llm.RenderErrorChain(e)
	// Suppress duplicate rendering so 'dup: dup' becomes 'dup'.
	if got != "dup" {
		t.Fatalf("repeated cause should be suppressed, got %q", got)
	}
}

func TestR05Aggregate(t *testing.T) {
	m := errors.Join(errors.New("a"), errors.New("b"), errors.New("c"))
	got := llm.RenderErrorChain(m)
	// errors.Join includes member text in Error and exposes members through Unwrap; rendering must
	// still include the bracketed member structure.
	if !contains(got, "[a; b; c]") {
		t.Fatalf("aggregate 应含括号成员组, got %q", got)
	}
}

func TestR05MessageFieldAndStr(t *testing.T) {
	// Extract a message field from a non-error value.
	if got := llm.RenderErrorChain(struct{ Message string }{Message: "hi msg"}); got != "hi msg" {
		t.Fatalf("message field = %q", got)
	}
	// Without a message field, stringify the value directly.
	if got := llm.RenderErrorChain(42); got != "42" {
		t.Fatalf("plain value = %q", got)
	}
}

func TestR05CircularAndEmpty(t *testing.T) {
	// Cause cycle: A -> B -> A.
	var a circularA
	var b circularB
	a.next = &b
	b.next = &a
	got := llm.RenderErrorChain(&a)
	if !contains(got, "<circular cause>") {
		t.Fatalf("circular 应含 <circular cause>, got %q", got)
	}
	// An empty message falls back to the type name.
	if got := llm.RenderErrorChain(simpleErr{}); got != "simpleErr" {
		t.Fatalf("empty message should fall back to type name, got %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
