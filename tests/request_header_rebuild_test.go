// M19 request-header snapshots and request/context routing tests.
package tests

import (
	"encoding/json"
	"testing"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// TestRequestHeaderRebuild reconstructs the actual payload from the latest header snapshot.
func TestRequestHeaderRebuild(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("rh_1"))

	// The second header snapshot supersedes the first.
	_, _ = sl.Append(session.RequestHeaderData{ConfigEpoch: 1, SystemHash: "old", ToolCount: 2})
	_, _ = sl.Append(session.RequestHeaderData{ConfigEpoch: 2, SystemHash: "new", ToolCount: 3})
	_, _ = sl.Append(session.RequestContextData{Provider: "deepseek", Model: "deepseek-chat", Window: 64000, Reason: "initial"})

	header, ok := session.FoldEpochHeader(sl.Events())
	if !ok {
		t.Fatal("应折叠到 header")
	}
	if header.ConfigEpoch != 2 || header.SystemHash != "new" || header.ToolCount != 3 {
		t.Fatalf("header 快照应为最新: %+v", header)
	}

	ctx, ok := session.FoldRequestContext(sl.Events())
	if !ok {
		t.Fatal("应折叠到 context")
	}
	if ctx.Provider != "deepseek" || ctx.Model != "deepseek-chat" || ctx.Reason != session.ReasonInitial {
		t.Fatalf("context 路由异常: %+v", ctx)
	}

	// The rebuilt payload must match the actual payload.
	snap := &session.RequestHeaderSnapshot{
		Header:       header,
		Context:      ctx,
		SystemPrompt: "system text",
		ToolSchemas:  json.RawMessage(`[{"name":"bash"}]`),
	}
	system, schemas, err := session.RebuildFromHeader(snap)
	if err != nil {
		t.Fatalf("RebuildFromHeader 失败: %v", err)
	}
	if system != "system text" {
		t.Fatalf("rebuild system 不一致: %q", system)
	}
	if string(schemas) != `[{"name":"bash"}]` {
		t.Fatalf("rebuild schemas 不一致: %s", schemas)
	}
}

// TestRequestHeaderCompactionRebuild reconstructs requests after compaction without original
// history.
func TestRequestHeaderCompactionRebuild(t *testing.T) {
	sl := session.NewSessionLog(brand.NewSessionID("rh_2"))
	_, _ = sl.Append(session.RequestHeaderData{ConfigEpoch: 5, SystemHash: "h5", ToolCount: 4})

	// Simulate compaction by retaining only the header snapshot event.
	events := sl.Events()

	header, ok := session.FoldEpochHeader(events)
	if !ok {
		t.Fatal("compaction 后仍应能折叠 header")
	}
	if header.SystemHash != "h5" {
		t.Fatalf("rebuild 应基于快照: %+v", header)
	}
}
