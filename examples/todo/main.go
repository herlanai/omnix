// Todo tutorial: each todo_write replaces the entire list with last-write-wins semantics, matching
// upstream. Goal manages the objective state machine; Todo manages its checklist. Write a list,
// derive it from the event log, then write again to observe complete replacement rather than
// item-by-item merging. Run: go run ./examples/todo. See pkg/todo/todo.go.
package main

import (
	"context"
	"fmt"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/todo"
)

func main() {
	ctx := context.Background()
	// 1. Create the session log from which Todo state is derived.
	sl := session.NewSessionLog(brand.NewSessionID("todo-demo"))
	// 2. Bind the todo_write tool to the log.
	tw := todo.NewTodoWriteTool(sl)

	// Write the first list. Input has the shape { items: [{content, done?}] } and replaces the whole
	// list.
	if _, err := tw.Execute(ctx, map[string]any{
		"items": []any{
			map[string]any{"content": "整理需求", "done": false},
			map[string]any{"content": "设计接口", "done": false},
			map[string]any{"content": "实现原型", "done": true},
		},
	}); err != nil {
		panic(err)
	}
	fmt.Println("— 第一次写入后（3 项）—")
	dump(todo.Current(sl))

	// Write two items to replace the previous three completely; completed items are neither retained
	// nor merged.
	if _, err := tw.Execute(ctx, map[string]any{
		"items": []any{
			map[string]any{"content": "补充测试", "done": false},
			map[string]any{"content": "文档收尾", "done": false},
		},
	}); err != nil {
		panic(err)
	}
	fmt.Println("— 第二次写入后（2 项，旧列表被整体替换）—")
	dump(todo.Current(sl))

	fmt.Println("\n— 结论：Todo = 整体替换清单，读自事件日志(派生)，与 Goal 互补 —")
}

// dump prints the current todo list.
func dump(items []todo.TodoItem) {
	for i, it := range items {
		fmt.Printf("  %d. [%s] %s\n", i+1, it.ResolvedStatus(), it.Content)
	}
}
