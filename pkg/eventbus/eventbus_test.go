package eventbus

import (
	"reflect"
	"sync"
	"testing"
)

func BenchmarkEmit(b *testing.B) {
	bus := New[int, int]()
	for i := 0; i < 16; i++ {
		bus.On(func(v int) int { return v })
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Emit(i)
	}
}

func TestDispatchUsesRegistrationSnapshot(t *testing.T) {
	bus := New[int, int]()
	var got []int
	var removeSecond func()
	bus.On(func(v int) int {
		got = append(got, 1)
		removeSecond()
		bus.On(func(v int) int { got = append(got, 3); return v })
		return v
	})
	removeSecond = bus.On(func(v int) int { got = append(got, 2); return v })
	bus.Emit(0)
	if !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("current dispatch changed: %v", got)
	}
	got = nil
	bus.Emit(0)
	if !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("next dispatch missed changes: %v", got)
	}
}

func TestConcurrentRegistrationAndDispatch(t *testing.T) {
	bus := New[int, int]()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				remove := bus.On(func(v int) int { return v })
				bus.Emit(j)
				remove()
				remove()
			}
		}()
	}
	wg.Wait()
	if got := bus.Len(); got != 0 {
		t.Fatalf("listeners leaked: %d", got)
	}
}
