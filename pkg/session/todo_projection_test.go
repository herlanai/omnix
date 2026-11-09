package session

import (
	"github.com/herlanai/omnix/pkg/brand"
	"testing"
)

func TestTodoProjectionRecordsStatusChanges(t *testing.T) {
	registry := NewProjectionRegistry()
	projection, err := RegisterProjection(registry, &ProjectionDefinition[TodoFold]{
		ID:    brand.NewProjectionID("todo-status"),
		Apply: ApplyTodoWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"pending", "in_progress", "completed"} {
		registry.ApplyEvents([]SessionEvent{{Seq: uint64(i + 1), Type: EventTodoWrite, Data: TodoWriteData{
			Items: []string{"task"}, Entries: []TodoEntry{{Content: "task", Status: status}},
		}}})
		state := projection.State()
		if len(state.Entries) != 1 || state.Entries[0].Status != status {
			t.Fatalf("status %q was lost: %+v", status, state)
		}
	}
	for _, change := range registry.Changelog() {
		if !change.Changed {
			t.Fatalf("status change was not recorded: %+v", change)
		}
	}
}

func TestTodoFoldEqualChecksEntries(t *testing.T) {
	initial := TodoFold{Present: true, Entries: []TodoEntry{{Content: "first", Status: "pending"}}}
	for _, other := range []TodoFold{
		{Present: true},
		{Present: true, Entries: []TodoEntry{{Content: "second", Status: "pending"}}},
		{Present: true, Entries: []TodoEntry{{Content: "first", Status: "completed"}}},
	} {
		if initial.Equal(other) {
			t.Fatalf("different entries compare equal: %+v", other)
		}
	}
	if !initial.Equal(initial) {
		t.Fatal("equal entries compare unequal")
	}
}
