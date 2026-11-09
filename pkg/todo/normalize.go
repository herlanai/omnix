// Todo normalization enforces constraints beyond schema: nonblank trimmed content,
// unique content, and at most one in_progress item without parallel mode. It also
// provides counts for all three states.
package todo

import (
	"errors"
	"strings"
)

// ErrEmptyContent indicates an item with blank content.
var ErrEmptyContent = errors.New("todo: empty content")

// ErrDuplicateContent indicates repeated content.
var ErrDuplicateContent = errors.New("todo: duplicate content")

// ErrMultipleActive indicates multiple in_progress items without parallel mode.
var ErrMultipleActive = errors.New("todo: more than one in_progress")

// Normalize trims and validates todos. Blank content returns ErrEmptyContent,
// duplicates return ErrDuplicateContent, and multiple active items without
// allowParallel return ErrMultipleActive.
func Normalize(items []TodoItem, allowParallel bool) ([]TodoItem, error) {
	out := make([]TodoItem, 0, len(items))
	seen := map[string]struct{}{}
	active := 0
	for _, it := range items {
		content := strings.TrimSpace(it.Content)
		if content == "" {
			return nil, ErrEmptyContent
		}
		if _, dup := seen[content]; dup {
			return nil, ErrDuplicateContent
		}
		seen[content] = struct{}{}
		status := it.ResolvedStatus()
		if status == StatusInProgress {
			active++
		}
		out = append(out, TodoItem{ID: it.ID, Content: content, Status: status})
	}
	if !allowParallel && active > 1 {
		return nil, ErrMultipleActive
	}
	return out, nil
}

// Counts holds counts for the three states.
type Counts struct {
	Pending    int
	InProgress int
	Completed  int
}

// Count counts items in each state.
func Count(items []TodoItem) Counts {
	var c Counts
	for _, it := range items {
		switch it.ResolvedStatus() {
		case StatusInProgress:
			c.InProgress++
		case StatusCompleted:
			c.Completed++
		default:
			c.Pending++
		}
	}
	return c
}
