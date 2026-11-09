// Event bus tests verify Emit broadcasts and ignores results, Bail short-circuits at the first
// match, Serial aggregates in registration order, and On's disposer removes listeners for plugin
// cleanup.
package tests

import (
	"errors"
	"testing"

	"github.com/herlanai/omnix/pkg/eventbus"
)

func TestEventBusEmitRunsAll(t *testing.T) {
	bus := eventbus.New[string, struct{}]()
	var got []string
	bus.On(func(s string) struct{} { got = append(got, "a:"+s); return struct{}{} })
	bus.On(func(s string) struct{} { got = append(got, "b:"+s); return struct{}{} })

	bus.Emit("x")

	if len(got) != 2 || got[0] != "a:x" || got[1] != "b:x" {
		t.Fatalf("emit should run all listeners in order, got %v", got)
	}
}

func TestEventBusBailShortCircuits(t *testing.T) {
	bus := eventbus.New[int, error]()
	var reached int
	bus.On(func(n int) error { reached = 1; return nil }) // No match.
	hit := errors.New("boom")
	bus.On(func(n int) error { reached = 2; return hit }) // Match and short-circuit.
	bus.On(func(n int) error { reached = 3; return nil }) // Must not be reached.

	r, ok := bus.Bail(7, func(e error) bool { return e != nil })

	if !ok || !errors.Is(r, hit) {
		t.Fatalf("bail should return first truthy result, got %v ok=%v", r, ok)
	}
	if reached != 2 {
		t.Fatalf("bail should short-circuit before third listener, reached=%d", reached)
	}
}

func TestEventBusBailNoHitReturnsFalse(t *testing.T) {
	bus := eventbus.New[int, error]()
	bus.On(func(n int) error { return nil })
	_, ok := bus.Bail(1, func(e error) bool { return e != nil })
	if ok {
		t.Fatal("bail without a hit should report false")
	}
}

func TestEventBusSerialAggregates(t *testing.T) {
	bus := eventbus.New[int, int]()
	bus.On(func(n int) int { return n + 1 })
	bus.On(func(n int) int { return n + 2 })

	out := bus.Serial(10)

	if len(out) != 2 || out[0] != 11 || out[1] != 12 {
		t.Fatalf("serial should aggregate in registration order, got %v", out)
	}
}

func TestEventBusDisposeRemovesListener(t *testing.T) {
	bus := eventbus.New[string, struct{}]()
	var count int
	dispose := bus.On(func(s string) struct{} { count++; return struct{}{} })

	bus.Emit("x")
	dispose()
	bus.Emit("x")

	if count != 1 {
		t.Fatalf("dispose should remove listener, got count=%d", count)
	}
}

func TestEventBusDisposeIdempotent(t *testing.T) {
	bus := eventbus.New[string, struct{}]()
	dispose := bus.On(func(s string) struct{} { return struct{}{} })
	dispose()
	dispose() // Must not panic.
	if bus.Len() != 0 {
		t.Fatalf("double dispose should keep len 0, got %d", bus.Len())
	}
}

func TestSignalEmit(t *testing.T) {
	sig := eventbus.NewSignal[int]()
	var sum int
	sig.On(func(n int) { sum += n })
	sig.On(func(n int) { sum += n })
	sig.Emit(3)
	if sum != 6 {
		t.Fatalf("signal should notify all, sum=%d", sum)
	}
}
