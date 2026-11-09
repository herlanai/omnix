// Surface operations define the read-time view of SessionEvent history (M21).
package session

import (
	"encoding/json"
	"slices"
	"sort"
)

// SurfaceOpKind identifies a surface operation.
type SurfaceOpKind string

// Supported surface operations.
const (
	// SurfaceAppend is the default append operation and need not be specified explicitly.
	SurfaceAppend SurfaceOpKind = "append"
	// SurfaceReplace replaces an inclusive historical range at read time.
	// Source events remain immutable; compaction changes only the derived view.
	SurfaceReplace SurfaceOpKind = "replace"
)

// SurfaceOp describes a persisted operation on the session's visible history.
// For replace operations, Start and End bound the inclusive source sequence range
// and Data holds the replacement payload. The operation is applied when reading
// SessionEvent.surfaceOp; it never rewrites the source events.
type SurfaceOp struct {
	Op    SurfaceOpKind   `json:"op"`
	Start uint64          `json:"start,omitempty"`
	End   uint64          `json:"end,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// NewAppendOp constructs an explicit append marker.
func NewAppendOp() *SurfaceOp {
	return &SurfaceOp{Op: SurfaceAppend}
}

// NewReplaceOp constructs a replacement marker.
func NewReplaceOp(start, end uint64, data json.RawMessage) *SurfaceOp {
	return &SurfaceOp{Op: SurfaceReplace, Start: start, End: end, Data: data}
}

// Read-time surface folding (M21).

// SurfaceNode is a visible original or replacement event after surface folding.
type SurfaceNode struct {
	// Seq is the event sequence number.
	Seq uint64
	// Replaced reports whether this node carries a replace operation.
	Replaced bool
	// Event is the complete event represented by this node.
	Event SessionEvent
}

// ReplaceRange is an inclusive sequence range covered by a surface replacement.
type ReplaceRange struct {
	Start uint64 `json:"start"`
	End   uint64 `json:"end"`
}

// FoldSurface applies surface replacements at read time and returns visible nodes
// and all replacement ranges for auditing. Source events remain unchanged.
// Like EffectiveEvents, visibility uses ranges clipped to the replacing event's
// sequence; the audit ranges retain their original bounds and event order.
func FoldSurface(events []SessionEvent) (nodes []SurfaceNode, replacements []ReplaceRange) {
	hidden := replacementIntervals(events)
	for _, ev := range events {
		if ev.SurfaceOp != nil && ev.SurfaceOp.Op == SurfaceReplace {
			replacements = append(replacements, ReplaceRange{Start: ev.SurfaceOp.Start, End: ev.SurfaceOp.End})
		}
	}
	for _, ev := range events {
		if hidden.contains(ev.Seq) {
			continue
		}
		replaced := ev.SurfaceOp != nil && ev.SurfaceOp.Op == SurfaceReplace
		nodes = append(nodes, SurfaceNode{Seq: ev.Seq, Replaced: replaced, Event: ev})
	}
	return nodes, replacements
}

// sequenceIntervals stores sorted, disjoint inclusive ranges. Its memory usage
// depends on the number of replacements, not the distance between sequence IDs.
type sequenceIntervals []ReplaceRange

func replacementIntervals(events []SessionEvent) sequenceIntervals {
	var ranges sequenceIntervals
	for _, ev := range events {
		op := ev.SurfaceOp
		if op == nil || op.Op != SurfaceReplace {
			continue
		}
		end := min(op.End, ev.Seq)
		if op.Start <= end {
			ranges = append(ranges, ReplaceRange{Start: op.Start, End: end})
		}
	}
	slices.SortFunc(ranges, func(a, b ReplaceRange) int {
		if a.Start < b.Start {
			return -1
		}
		if a.Start > b.Start {
			return 1
		}
		return 0
	})
	merged := ranges[:0]
	for _, r := range ranges {
		if len(merged) == 0 || r.Start > merged[len(merged)-1].End {
			merged = append(merged, r)
		} else {
			last := &merged[len(merged)-1]
			last.End = max(last.End, r.End)
		}
	}
	return merged
}

func (ranges sequenceIntervals) contains(seq uint64) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].End >= seq })
	return i < len(ranges) && ranges[i].Start <= seq
}
