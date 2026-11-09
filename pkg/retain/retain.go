// Package retain implements bounded logical-item retention from util/output-retention.
// Push keeps the first maxItems units and counts all omitted units exactly. Finish
// returns retained items and omission metadata without tool-specific semantics.
package retain

// Result describes retained and omitted items.
type Result[T any] struct {
	Items     []T  // Retained subset.
	Seen      int  // Total observed items.
	Omitted   int  // Items omitted due to the budget.
	Truncated bool // Whether any items were omitted.
}

// ItemRetainer keeps the first maxItems logical units.
type ItemRetainer[T any] struct {
	maxItems int
	items    []T
	seen     int
	omitted  int
}

// NewItemRetainer creates a head retainer; negative limits become zero.
func NewItemRetainer[T any](maxItems int) *ItemRetainer[T] {
	if maxItems < 0 {
		maxItems = 0
	}
	return &ItemRetainer[T]{maxItems: maxItems}
}

// Push observes an item, retaining it if capacity remains or counting its omission.
// It reports whether the item was retained.
func (r *ItemRetainer[T]) Push(item T) bool {
	r.seen++
	if len(r.items) < r.maxItems {
		r.items = append(r.items, item)
		return true
	}
	r.omitted++
	return false
}

// Finish returns the retention result.
func (r *ItemRetainer[T]) Finish() Result[T] {
	return Result[T]{
		Items:     r.items,
		Seen:      r.seen,
		Omitted:   r.omitted,
		Truncated: r.omitted > 0,
	}
}
