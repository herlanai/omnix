package session

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func BenchmarkEffectiveEvents(b *testing.B) {
	for _, replace := range []bool{false, true} {
		b.Run(fmt.Sprintf("replace=%t", replace), func(b *testing.B) {
			events := make([]SessionEvent, 10000)
			for i := range events {
				events[i].Seq = uint64(i + 1)
			}
			if replace {
				events[len(events)-1].SurfaceOp = NewReplaceOp(1, 9000, nil)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = EffectiveEvents(events)
			}
		})
	}
}

func TestSurfaceReplacementBoundaries(t *testing.T) {
	max := ^uint64(0)
	tests := []struct {
		name   string
		events []SessionEvent
		want   []uint64
	}{
		{"sparse", []SessionEvent{{Seq: 1}, {Seq: max - 1}, {Seq: max, SurfaceOp: NewReplaceOp(1, max-1, nil)}}, []uint64{max}},
		{"maximum", []SessionEvent{{Seq: max, SurfaceOp: NewReplaceOp(max, max, nil)}}, nil},
		{"inverted", []SessionEvent{{Seq: 1}, {Seq: 2, SurfaceOp: NewReplaceOp(2, 1, nil)}}, []uint64{1, 2}},
		{"clipped", []SessionEvent{{Seq: 1}, {Seq: 3, SurfaceOp: NewReplaceOp(1, 100, nil)}, {Seq: 4}}, []uint64{4}},
		{"overlap", []SessionEvent{{Seq: 1}, {Seq: 2}, {Seq: 3}, {Seq: 4, SurfaceOp: NewReplaceOp(1, 2, nil)}, {Seq: 5, SurfaceOp: NewReplaceOp(2, 3, nil)}}, []uint64{4, 5}},
		{"unordered", []SessionEvent{{Seq: 5}, {Seq: 1}, {Seq: 4, SurfaceOp: NewReplaceOp(1, 2, nil)}, {Seq: 3}}, []uint64{5, 4, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []uint64
			for _, ev := range EffectiveEvents(tt.events) {
				got = append(got, ev.Seq)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("EffectiveEvents: got %v, want %v", got, tt.want)
			}
			nodes, ranges := FoldSurface(tt.events)
			got = nil
			for _, node := range nodes {
				got = append(got, node.Seq)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FoldSurface: got %v, want %v", got, tt.want)
			}
			var wantRanges []ReplaceRange
			for _, ev := range tt.events {
				if ev.SurfaceOp != nil {
					wantRanges = append(wantRanges, ReplaceRange{Start: ev.SurfaceOp.Start, End: ev.SurfaceOp.End})
				}
			}
			if !reflect.DeepEqual(ranges, wantRanges) {
				t.Fatalf("replacement audit changed: %v", ranges)
			}
		})
	}
}

// Compare against the original dense representation over bounded random ranges.
func TestSurfaceReplacementMatchesDenseReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		events := make([]SessionEvent, rng.Intn(100))
		hidden := map[uint64]bool{}
		for i := range events {
			ev := &events[i]
			ev.Seq = uint64(rng.Intn(100))
			if rng.Intn(3) == 0 {
				ev.SurfaceOp = NewReplaceOp(uint64(rng.Intn(100)), uint64(rng.Intn(100)), nil)
				for seq := ev.SurfaceOp.Start; seq <= ev.SurfaceOp.End && seq <= ev.Seq; seq++ {
					hidden[seq] = true
				}
			}
		}
		want := make([]SessionEvent, 0, len(events))
		for _, ev := range events {
			if !hidden[ev.Seq] {
				want = append(want, ev)
			}
		}
		got := EffectiveEvents(events)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: visible events differ", trial)
		}
		nodes, _ := FoldSurface(events)
		fromNodes := make([]SessionEvent, 0, len(nodes))
		for _, node := range nodes {
			fromNodes = append(fromNodes, node.Event)
		}
		if !reflect.DeepEqual(fromNodes, want) {
			t.Fatalf("trial %d: surface nodes differ", trial)
		}
	}
}
