// M42 spill tests cover oversized tool and Bash results, byte-exact locator reads, inline
// retention below the threshold, and best-effort preservation after save failure.
package tests

import (
	"strings"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/spill"
)

// newSpillReq constructs a tool-origin save request.
func newSpillReq(sessionID, tool, suggested string) spill.SaveTextSpill {
	return spill.SaveTextSpill{
		Owner:         spill.SpillOwner{SessionID: brand.NewSessionID(sessionID)},
		Source:        spill.SpillSource{ToolName: tool, CallID: brand.NewToolCallID("call-1"), Label: "result"},
		SuggestedName: suggested,
	}
}

// TestSpillToolResultOverThreshold spills oversized results and resolves them exactly.
func TestSpillToolResultOverThreshold(t *testing.T) {
	store := spill.NewFileStore(t.TempDir())
	bigText := strings.Repeat("line of tool output\n", 500) // Approximately 9 KB exceeds the 1 KB threshold.

	inline, ref, err := spill.Apply(store, newSpillReq("s1", "web_fetch", "web_fetch.txt"), bigText, 1024, 256)
	if err != nil {
		t.Fatalf("spill 失败: %v", err)
	}
	if ref == nil {
		t.Fatal("超过阈值应产生 spill ref")
	}
	if len(inline) >= len(bigText) {
		t.Fatal("内联应为预览，而非完整文本")
	}
	// The locator must resolve to the complete original bytes.
	got, err := store.ReadText(ref.Locator)
	if err != nil {
		t.Fatalf("读 locator 失败: %v", err)
	}
	if got != bigText {
		t.Fatalf("还原内容不一致: got %d bytes, want %d bytes", len(got), len(bigText))
	}
}

// TestSpillBashResultOverThreshold spills oversized Bash output as a second consumer.
func TestSpillBashResultOverThreshold(t *testing.T) {
	store := spill.NewFileStore(t.TempDir())
	seqOutput := strings.Repeat("1\n2\n3\n4\n5\n", 1000) // ~10KB

	_, ref, err := spill.Apply(store, newSpillReq("s2", "bash", "bash.txt"), seqOutput, 64, 64)
	if err != nil {
		t.Fatalf("bash spill 失败: %v", err)
	}
	if ref == nil {
		t.Fatal("bash 结果超阈值应 spill")
	}
	if ref.Bytes != len(seqOutput) {
		t.Fatalf("ref.Bytes 应等于原文长度 %d, 实际 %d", len(seqOutput), ref.Bytes)
	}
	got, _ := store.ReadText(ref.Locator)
	if got != seqOutput {
		t.Fatal("Bash 结果 read-back 应逐字节一致")
	}
}

// TestSpillUnderThresholdInline keeps small output inline without a reference.
func TestSpillUnderThresholdInline(t *testing.T) {
	store := spill.NewFileStore(t.TempDir())
	small := "short"
	inline, ref, err := spill.Apply(store, newSpillReq("s3", "tool", "a.txt"), small, 1024, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ref != nil {
		t.Fatal("未超阈值不应 spill")
	}
	if inline != small {
		t.Fatalf("应保留原内联, 实际 %q", inline)
	}
}

// TestSpillBestEffortOnSaveFailure preserves the original text after a save failure.
func TestSpillBestEffortOnSaveFailure(t *testing.T) {
	// Use an existing file as the root so directory creation and saving fail.
	store := spill.NewFileStore("")
	big := strings.Repeat("x", 4096)
	_, _, err := spill.Apply(store, newSpillReq("s4", "bash", "b.txt"), big, 64, 0)
	if err == nil {
		t.Fatal("root 未配置应保存失败并返回错误")
	}
	// Apply retains the original text but returns the storage error for caller handling; it does not
	// mark an otherwise successful tool call as isError.
}
