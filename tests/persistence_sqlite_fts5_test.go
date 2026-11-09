// S03 SQLite/FTS5 tests cover atomic 10000-event batches, ordered round trips, title/message
// search, large-dataset correctness, and separate query benchmarks.
package tests

import (
	"context"
	"testing"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	sqlstore "github.com/herlanai/omnix/pkg/persistence/sqlite"
	"github.com/herlanai/omnix/pkg/session"
)

// buildEvents constructs n user messages with occasional searchable keywords.
func buildEvents(n int) []session.SessionEvent {
	evs := make([]session.SessionEvent, 0, n)
	for i := 1; i <= n; i++ {
		msg := "普通用户消息编号 " + time.Now().Format("150405") + " 内容填充补足长度"
		if i%500 == 0 {
			msg = "需要检索的主题词：数据库选型对比报告"
		}
		evs = append(evs, session.SessionEvent{
			Seq:  uint64(i),
			Time: time.Now().Add(time.Duration(i) * time.Microsecond),
			Type: session.EventUserMessage,
			Data: session.UserMessageData{Content: msg},
		})
	}
	return evs
}

// TestSQLiteWriteReadRoundtrip verifies a 10000-event batch round trip.
func TestSQLiteWriteReadRoundtrip(t *testing.T) {
	ctx := context.Background()
	st, err := sqlstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sid := brand.NewSessionID("s-1k")
	const n = 10000
	if err := st.AppendBatch(ctx, sid, buildEvents(n)); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.Count(ctx, sid); c != n {
		t.Fatalf("应有 %d 条事件，实际 %d", n, c)
	}
	loaded, err := st.Load(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != n {
		t.Fatalf("读回 %d != %d", len(loaded), n)
	}
	// Sequences must increase strictly from 1 through n.
	for i, ev := range loaded {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seq 应 %d，实际 %d", i+1, ev.Seq)
		}
	}
}

// TestSQLiteFTSSearch matches title and message content.
func TestSQLiteFTSSearch(t *testing.T) {
	ctx := context.Background()
	st, err := sqlstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sid := brand.NewSessionID("s-fts")
	// Write events containing titles and keyword-bearing messages.
	for _, ev := range buildEvents(2000) {
		if err := st.Append(ctx, sid, ev); err != nil {
			t.Fatal(err)
		}
	}
	// Add a distinct title and message for predictable search hits.
	_ = st.Append(ctx, sid, session.SessionEvent{
		Seq: 3001, Time: time.Now(), Type: session.EventSessionTitle,
		Data: session.SessionTitleData{Title: "竞品分析与缓存命中率对齐方案"},
	})
	_ = st.Append(ctx, sid, session.SessionEvent{
		Seq: 3002, Time: time.Now(), Type: session.EventUserMessage,
		Data: session.UserMessageData{Content: "请分析数据库选型对比报告"},
	})

	// Match a title keyword through FTS.
	if !st.FTSEnabled() {
		t.Skip("当前 SQLite 驱动不支持 FTS5")
	}
	matches, err := st.Search(ctx, "缓存命中率")
	if err != nil {
		t.Fatal(err)
	}
	foundTitle := false
	for _, m := range matches {
		if m.Seq == 3001 {
			foundTitle = true
		}
	}
	if !foundTitle {
		t.Fatalf("FTS5 应命中 session/title 事件(seq=3001)，实际 matches=%+v", matches)
	}

	// Match a message keyword through FTS.
	matches2, err := st.Search(ctx, "数据库选型")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches2) == 0 {
		t.Fatal("FTS5 应命中消息内容中的关键词")
	}
}

// TestSQLiteSearchLargeDataset verifies results after a 10000-event batch.
func TestSQLiteSearchLargeDataset(t *testing.T) {
	ctx := context.Background()
	st, err := sqlstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sid := brand.NewSessionID("s-perf")
	if err := st.AppendBatch(ctx, sid, buildEvents(10000)); err != nil {
		t.Fatal(err)
	}

	matches, err := st.Search(ctx, "编号")
	if err != nil {
		t.Fatal(err)
	}
	// Every 500th event uses a different message without this keyword.
	if got, want := len(matches), 10000-10000/500; got != want {
		t.Fatalf("got %d matches, want %d", got, want)
	}
	for i, match := range matches {
		if match.SessionID != sid.Raw() || match.Seq%500 == 0 {
			t.Fatalf("unexpected match: %+v", match)
		}
		if i > 0 && matches[i-1].Seq >= match.Seq {
			t.Fatal("search results are not ordered")
		}
	}

}

// TestSQLiteSearchEmpty returns no results for an empty query.
func TestSQLiteSearchEmpty(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlstore.Open(":memory:")
	defer st.Close()
	ms, err := st.Search(ctx, "   ")
	if err != nil || len(ms) != 0 {
		t.Fatalf("空关键词应返回空，实际 %v err=%v", ms, err)
	}
}

// BenchmarkSQLiteSearch measures query latency without machine-dependent test deadlines.
func BenchmarkSQLiteSearch(b *testing.B) {
	ctx := context.Background()
	st, err := sqlstore.Open(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	if err := st.AppendBatch(ctx, brand.NewSessionID("benchmark"), buildEvents(10000)); err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name, query string
		count       int
	}{
		{"short-query", "编号", 9980},
		{"phrase-query", "数据库选型", 20},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				matches, err := st.Search(ctx, tc.query)
				if err != nil {
					b.Fatal(err)
				}
				if len(matches) != tc.count {
					b.Fatalf("got %d matches, want %d", len(matches), tc.count)
				}
			}
		})
	}
}
