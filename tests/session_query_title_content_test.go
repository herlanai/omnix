// S04 session-query and FTS5 tests cover combined title/time/keyword filtering, content-based
// session lookup, and accurate result summaries.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	sqlstore "github.com/herlanai/omnix/pkg/persistence/sqlite"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sessionquery"
)

// seedSessions writes three titled sessions to SQLite with fixed creation times for range
// filtering.
func seedSessions(t *testing.T, ctx context.Context, st *sqlstore.Store) {
	t.Helper()
	base := time.Now().Add(-24 * time.Hour)
	specs := []struct {
		id    string
		title string
		msg   string
		hour  int
	}{
		{"s1", "数据库选型对比报告", "分析 MySQL 与 PostgreSQL 的差异", 2},
		{"s2", "缓存命中率对齐方案", "如何达到 99% cache hit rate", 6},
		{"s3", "部署上线流程", "用 docker 部署服务", 10},
	}
	for _, sp := range specs {
		sid := brand.NewSessionID(sp.id)
		// A user message at sequence 1 uses the beginning of the selected hour.
		msgEv := session.SessionEvent{
			Seq: 1, Time: base.Add(time.Duration(sp.hour) * time.Hour),
			Type: session.EventUserMessage, Data: session.UserMessageData{Content: sp.msg},
		}
		titleEv := session.SessionEvent{
			Seq: 2, Time: msgEv.Time.Add(time.Second),
			Type: session.EventSessionTitle, Data: session.SessionTitleData{Title: sp.title},
		}
		if err := st.Append(ctx, sid, msgEv); err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, sid, titleEv); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSessionQueryTripleFilter combines title prefix, creation-time range, and limit.
func TestSessionQueryTripleFilter(t *testing.T) {
	ctx := context.Background()
	st, err := sqlstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedSessions(t, ctx, st)
	q := sessionquery.New(st)

	from := time.Now().Add(-24 * time.Hour).Add(5 * time.Hour)
	to := time.Now().Add(-24 * time.Hour).Add(11 * time.Hour)

	// Filter by the data-related title prefix, excluding the cache-titled session.
	list := mustList(t, q, sessionquery.ListRequest{TitlePrefix: "数据"})
	if len(list) != 1 || list[0].ID != "s1" {
		t.Fatalf("标题前缀『数据』应仅命中 s1，实际 %+v", list)
	}

	// The range [5h,11h) includes s2 at 6h and s3 at 10h, excluding s1 at 2h.
	list2 := mustList(t, q, sessionquery.ListRequest{CreatedFrom: &from, CreatedTo: &to})
	ids := idsOf(list2)
	if len(ids) != 2 || !has(ids, "s2") || !has(ids, "s3") {
		t.Fatalf("时间范围应命中 s2/s3，实际 %v", ids)
	}

	// Combine filters with Limit=1; descending update order selects s3.
	list3 := mustList(t, q, sessionquery.ListRequest{TitlePrefix: "", Limit: 1})
	if len(list3) != 1 || list3[0].ID != "s3" {
		t.Fatalf("Limit=1 且倒序应命中最新 s3，实际 %+v", list3)
	}
}

// TestSessionQueryContentSearch locates sessions through FTS5 keywords.
func TestSessionQueryContentSearch(t *testing.T) {
	ctx := context.Background()
	st, err := sqlstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedSessions(t, ctx, st)
	q := sessionquery.New(st)

	if !st.FTSEnabled() {
		t.Skip("当前驱动不支持 FTS5")
	}
	// PostgreSQL in message content matches s1.
	res := mustSearch(t, q, sessionquery.SearchRequest{Keyword: "PostgreSQL"})
	if len(res) != 1 || res[0].ID != "s1" {
		t.Fatalf("关键词 PostgreSQL 应命中 s1，实际 %+v", res)
	}
	// The cache keyword in the title matches s2.
	res2 := mustSearch(t, q, sessionquery.SearchRequest{Keyword: "缓存命中率"})
	if len(res2) != 1 || res2[0].ID != "s2" {
		t.Fatalf("标题含『缓存命中率』应命中 s2，实际 %+v", res2)
	}
}

// TestSessionQueryEmptyKeyword returns no results for an empty query.
func TestSessionQueryEmptyKeyword(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlstore.Open(":memory:")
	defer st.Close()
	q := sessionquery.New(st)
	res, err := q.Search(ctx, sessionquery.SearchRequest{Keyword: "  "})
	if err != nil || len(res) != 0 {
		t.Fatalf("空关键词应返回空，实际 %v err=%v", res, err)
	}
}

// Helpers.

func mustList(t *testing.T, q *sessionquery.QueryService, req sessionquery.ListRequest) []sessionquery.SessionSummary {
	t.Helper()
	list, err := q.ListSummaries(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func mustSearch(t *testing.T, q *sessionquery.QueryService, req sessionquery.SearchRequest) []sessionquery.SessionSummary {
	t.Helper()
	res, err := q.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func idsOf(list []sessionquery.SessionSummary) map[string]bool {
	m := map[string]bool{}
	for _, s := range list {
		m[s.ID] = true
	}
	return m
}

func has(m map[string]bool, id string) bool { return m[id] }
