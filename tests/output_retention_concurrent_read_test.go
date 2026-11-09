// S16 retention tests cover complete concurrent reads of a 10 MB canonical value, independent
// copies, absent reads returning false, and logical deletion before commitment.
package tests

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/tools"
)

// TestOutputRetentionConcurrentReaders gives two readers complete copies of the same 10 MB result.
func TestOutputRetentionConcurrentReaders(t *testing.T) {
	ctx := context.Background()
	_ = ctx
	ret := tools.NewResultRetention()

	// Construct a deterministic 10 MB value for integrity checks.
	const size = 10 * 1024 * 1024
	big := make([]byte, size)
	for i := range big {
		big[i] = byte(i % 251)
	}
	callID := brand.NewToolCallID("tc-big")
	ret.Retain(callID.Raw(), big)

	// Start two concurrent readers.
	var wg sync.WaitGroup
	results := make([][]byte, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			got, ok := ret.Read(callID.Raw())
			if !ok {
				results[idx] = nil
				return
			}
			results[idx] = got
		}(i)
	}
	wg.Wait()

	for i, got := range results {
		if got == nil {
			t.Fatalf("reader[%d] 未读到结果", i)
		}
		if len(got) != size {
			t.Fatalf("reader[%d] 长度 %d != %d（缺失）", i, len(got), size)
		}
		if !bytes.Equal(got, big) {
			t.Fatalf("reader[%d] 内容与原始不一致（截断/损坏）", i)
		}
	}
	// Readers receive independent backing arrays; modifying one must not affect the other.
	results[0][0] = ^results[0][0]
	if results[1][0] == results[0][0] {
		t.Fatal("两个 reader 应持有独立副本")
	}
}

// TestOutputRetentionRetainResult extracts canonical values, including JSON-encoded structs.
func TestOutputRetentionRetainResult(t *testing.T) {
	ret := tools.NewResultRetention()
	callID := brand.NewToolCallID("tc-json")
	res := &tools.ToolCallResult{
		CallID:  callID,
		IsError: false,
		Value:   map[string]any{"ok": true, "lines": 3},
	}
	ret.RetainResult(res)

	got, ok := ret.Read(callID.Raw())
	if !ok {
		t.Fatal("RetainResult 后应可读")
	}
	// Retain the complete serialized JSON bytes.
	if !bytes.HasPrefix(got, []byte("{")) || !bytes.Contains(got, []byte(`"lines":3`)) {
		t.Fatalf("保留值为预期 JSON，实际 %s", got)
	}
}

// TestOutputRetentionString retains strings as bytes.
func TestOutputRetentionString(t *testing.T) {
	ret := tools.NewResultRetention()
	ret.Retain("tc-str", []byte("beef"))
	got, ok := ret.Read("tc-str")
	if !ok || string(got) != "beef" {
		t.Fatalf("string 应保留原文，实际 %q ok=%v", got, ok)
	}
}

// TestOutputRetentionMissing returns false for an unretained value.
func TestOutputRetentionMissing(t *testing.T) {
	ret := tools.NewResultRetention()
	if _, ok := ret.Read("nope"); ok {
		t.Fatal("未保留的 callID 应返回 false")
	}
	// Reads after logical deletion return false.
	ret.Retain("gone", []byte("x"))
	ret.Remove("gone")
	if _, ok := ret.Read("gone"); ok {
		t.Fatal("逻辑删除后应返回 false")
	}
}
