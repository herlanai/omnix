// Package todo implements whole-list replacement, matching core/todo. Each
// todo/write replaces the current list with last-write-wins semantics.
// TodoWriteTool validates model JSON and writes a session event. Reads use
// FoldTodoWrite so incremental appends and cold replay agree.
package todo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/tools"
)

// TodoStatus is the three-state todo lifecycle matching upstream TodoItem.status.
type TodoStatus string

const (
	StatusPending    TodoStatus = "pending"     // Not started.
	StatusInProgress TodoStatus = "in_progress" // In progress.
	StatusCompleted  TodoStatus = "completed"   // Completed.
)

// Valid checks whether the status is one of the three valid states.
func (s TodoStatus) Valid() bool {
	return s == StatusPending || s == StatusInProgress || s == StatusCompleted
}

type TodoItem struct {
	// ID optionally tracks items across replacements; upstream does not require IDs.
	ID string `json:"id,omitempty"`
	// Content describes the todo.
	Content string `json:"content"`
	// Status is the three-state status.
	Status TodoStatus `json:"status,omitempty"`
	// Done provides two-state compatibility when Status is empty.
	Done bool `json:"done,omitempty"`
}

// ResolvedStatus prefers Status, otherwise derives it from Done.
func (it TodoItem) ResolvedStatus() TodoStatus {
	if it.Status.Valid() {
		return it.Status
	}
	if it.Done {
		return StatusCompleted
	}
	return StatusPending
}

// TodoWriteTool implements todo_write. Input contains items with optional IDs
// and legacy done flags. Each call completely replaces the list and appends
// a todo/write event to SessionLog.
type TodoWriteTool struct {
	// log receives todo/write events.
	log *session.SessionLog
	// AllowParallel permits multiple in_progress items; false defaults to sequential work.
	AllowParallel bool
}

// NewTodoWriteTool creates a todo_write tool.
func NewTodoWriteTool(sl *session.SessionLog) *TodoWriteTool {
	return &TodoWriteTool{log: sl}
}

// todoWriteInput describes tool arguments.
type todoWriteInput struct {
	Items []TodoItem `json:"items"`
}

// Name returns the tool name.
func (t *TodoWriteTool) Name() string { return "todo_write" }

// Description returns the tool description.
func (t *TodoWriteTool) Description() string { return "整体替换当前待办列表" }

// Execute replaces the todo list and writes its event.
func (t *TodoWriteTool) Execute(ctx context.Context, input map[string]any) (any, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("todo: marshal input: %w", err)
	}
	var in todoWriteInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("todo: invalid input: %w", err)
	}

	// Validate nonblank, unique content and the active-item limit.
	items, err := Normalize(in.Items, t.AllowParallel)
	if err != nil {
		return nil, err
	}

	// Build persisted three-state entries and the legacy Items field.
	entries := make([]session.TodoEntry, len(items))
	contents := make([]string, len(items))
	for i, it := range items {
		entries[i] = session.TodoEntry{Content: it.Content, Status: string(it.ResolvedStatus())}
		contents[i] = it.Content
	}

	// Emit todo/write as a last-write-wins whole-list replacement.
	if _, err := t.log.Append(session.TodoWriteData{Items: contents, Entries: entries}); err != nil {
		return nil, fmt.Errorf("todo: append: %w", err)
	}
	c := Count(items)
	return map[string]any{
		"ok": true,
		"counts": map[string]int{
			"pending": c.Pending, "inProgress": c.InProgress, "completed": c.Completed,
		},
	}, nil
}

// TodoTool returns a *tools.Tool wrapper for integration with M23 pipeline.
func (t *TodoWriteTool) Tool() *tools.Tool {
	return &tools.Tool{
		Name:        t.Name(),
		Description: t.Description(),
		Execute:     t.Execute,
	}
}

// Current derives the latest list through folding, preferring three-state entries.
func Current(log *session.SessionLog) []TodoItem {
	fold := session.FoldTodoWrite(log.Events())
	if len(fold.Entries) > 0 {
		out := make([]TodoItem, len(fold.Entries))
		for i, e := range fold.Entries {
			out[i] = TodoItem{Content: e.Content, Status: TodoStatus(e.Status)}
		}
		return out
	}
	out := make([]TodoItem, len(fold.Items))
	for i, c := range fold.Items {
		out[i] = TodoItem{Content: c, Status: StatusPending}
	}
	return out
}
