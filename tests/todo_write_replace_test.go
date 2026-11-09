// M13 whole-list todo replacement tests.
package tests

import (
	"context"
	"reflect"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/todo"
)

// TestTodoWriteReplace verifies last-write-wins replacement on every todo/write.
func TestTodoWriteReplace(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("todo_1"))
	tool := todo.NewTodoWriteTool(sl)

	// First whole-list replacement.
	_, err := tool.Execute(context.Background(), map[string]any{
		"items": []any{
			map[string]any{"content": "a"},
			map[string]any{"content": "b"},
		},
	})
	if err != nil {
		t.Fatalf("第一次执行失败: %v", err)
	}
	if got := contentsOf(todo.Current(sl)); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("第一次替换结果 = %v, want [a b]", got)
	}

	// Second replacement leaves only one item.
	_, err = tool.Execute(context.Background(), map[string]any{
		"items": []any{
			map[string]any{"content": "c"},
		},
	})
	if err != nil {
		t.Fatalf("第二次执行失败: %v", err)
	}
	if got := contentsOf(todo.Current(sl)); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("第二次应整体替换为 [c], got %v", got)
	}

	// An empty replacement clears the list.
	_, err = tool.Execute(context.Background(), map[string]any{"items": []any{}})
	if err != nil {
		t.Fatalf("清空执行失败: %v", err)
	}
	if got := todo.Current(sl); len(got) != 0 {
		t.Fatalf("清空后应为空, got %v", got)
	}

	// Last-write-wins folding retains only the latest complete list.
	fold := session.FoldTodoWrite(sl.Events())
	if !fold.Present || len(fold.Items) != 0 {
		t.Fatalf("fold 应反映最新空列表: %+v", fold)
	}
}

// contentsOf extracts todo content strings.
func contentsOf(items []todo.TodoItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Content
	}
	return out
}
