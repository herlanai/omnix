// Sequence-range encoding follows upstream session semantics for compact
// sourceEventSeqs storage. Strictly increasing runs of at least three values
// become inclusive ranges; shorter runs remain singletons. Decoding is lossless.
package session

import "errors"

// SeqRange is inclusive; equal endpoints represent one sequence number.
type SeqRange struct {
	From int
	To   int
}

func strictlyIncreasing(values []int) bool {
	for i := 1; i < len(values); i++ {
		if values[i] <= values[i-1] {
			return false
		}
	}
	return true
}

// EncodeSeqRanges compresses strictly increasing values, otherwise preserving them as singletons.
func EncodeSeqRanges(values []int) []SeqRange {
	if !strictlyIncreasing(values) {
		out := make([]SeqRange, len(values))
		for i, v := range values {
			out[i] = SeqRange{v, v}
		}
		return out
	}
	var out []SeqRange
	for start := 0; start < len(values); {
		end := start
		for end+1 < len(values) && values[end+1] == values[end]+1 {
			end++
		}
		if end-start >= 2 {
			out = append(out, SeqRange{values[start], values[end]})
		} else {
			for i := start; i <= end; i++ {
				out = append(out, SeqRange{values[i], values[i]})
			}
		}
		start = end + 1
	}
	return out
}

// DecodeSeqRanges expands ranges losslessly; maxEntries <= 0 disables the entry limit.
func DecodeSeqRanges(ranges []SeqRange, maxEntries int) ([]int, error) {
	total := 0
	hadRange := false
	maxInt := int(^uint(0) >> 1)
	for _, r := range ranges {
		if r.From < 0 || r.To < r.From {
			return nil, errors.New("invalid seq range")
		}
		span := r.To - r.From
		hadRange = hadRange || span > 0
		// Compare before adding one so an inclusive range cannot overflow int.
		if maxEntries > 0 && span >= maxEntries-total {
			return nil, errors.New("seq ranges exceed max entries")
		}
		if span >= maxInt-total {
			return nil, errors.New("seq ranges exceed integer capacity")
		}
		total += span + 1
	}
	if hadRange {
		for i := 1; i < len(ranges); i++ {
			if ranges[i].From <= ranges[i-1].To {
				return nil, errors.New("decoded seq ranges must be strictly increasing")
			}
		}
	}
	if total == 0 {
		return nil, nil
	}
	out := make([]int, total)
	index := 0
	for _, r := range ranges {
		count := r.To - r.From + 1
		// Count offsets instead of incrementing the endpoint, which may be MaxInt.
		for offset := 0; offset < count; offset++ {
			out[index+offset] = r.From + offset
		}
		index += count
	}
	return out, nil
}
