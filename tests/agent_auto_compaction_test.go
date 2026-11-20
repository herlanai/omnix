// 透明自动压缩（参考 codex compaction）：当事件日志估算 token 超过阈值时，
// Agent 在 step 前自动执行 compaction，并以带 SurfaceOp 的 assistant/message
// 替换历史区间；源事件不可变，后续请求使用有效事件派生。
package tests

import (
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/compaction"
	"github.com/herlanai/omnix/pkg/llm"
	"github.com/herlanai/omnix/pkg/session"
)

// TestAgentTransparentAutoCompaction 验证：连续两个长 turn 触发自动压缩，
// 日志中出现带 SurfaceOp 的替换事件，且有效事件数收缩。
func TestAgentTransparentAutoCompaction(t *testing.T) {
	adapter := newScriptedAdapter()
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "第一轮回复"})
	adapter.script(llm.StreamChunk{Kind: llm.ChunkText, Text: "第二轮回复"})
	a := buildAgent(t, adapter)
	// 极小阈值：单条长输入即可超限，保证第二个 turn 前触发压缩。
	a.SetCompactor(compaction.New(compaction.Config{MaxTokens: 200, KeepTail: 2}, nil))

	long := strings.Repeat("很长的对话内容用于触发压缩。", 60) // ≈ 840 runes → ≈ 421 tokens
	if err := a.Run(long); err != nil {
		t.Fatalf("Run 第一轮失败: %v", err)
	}
	if !waitFor(func() bool { return countTurnEnds(a.Log()) == 1 }) {
		t.Fatal("第一轮 turn 未完成")
	}
	if err := a.Run(long); err != nil {
		t.Fatalf("Run 第二轮失败: %v", err)
	}
	if !waitFor(func() bool { return countTurnEnds(a.Log()) == 2 }) {
		t.Fatal("第二轮 turn 未完成")
	}

	// 日志中必须存在带 SurfaceOp 替换标记的事件。
	events := a.Log().Events()
	var hasReplacement bool
	for _, ev := range events {
		if ev.SurfaceOp != nil && ev.SurfaceOp.Op == session.SurfaceReplace {
			hasReplacement = true
			break
		}
	}
	if !hasReplacement {
		t.Fatal("自动压缩未产生带 SurfaceOp 的替换事件")
	}

	// 有效事件（投影后）应少于源事件。
	effective := session.EffectiveEvents(events)
	if len(effective) >= len(events) {
		t.Fatalf("有效事件数应收缩: effective=%d events=%d", len(effective), len(events))
	}

	// 两个 turn 都正常结束（压缩对调用方透明）。
	if adapter.callCount() != 2 {
		t.Fatalf("Chat 调用次数 = %d, want 2", adapter.callCount())
	}
}
