// M47 presentation tests cover call cards (generic, terminal, diff), result cards (generic,
// terminal, diff, search matches/paths, read, and web search/fetch), CardOf classification, and
// custom card types.
package tests

import (
	"testing"

	"github.com/herlanai/omnix/pkg/tools"
)

// customCard is a custom result-card type that does not implement tools.ToolResultView.
type customCard struct {
	Card string `json:"card"`
	Note string `json:"note"`
}

// TestToolPresentationAllCallCards verifies all three call-card types.
func TestToolPresentationAllCallCards(t *testing.T) {
	gen := tools.NewGenericCallView("Search Files")
	gen.Kind = tools.CallKindSearch
	cases := []struct {
		name string
		view tools.ToolCallView
		want string
	}{
		{"generic", gen, "generic"},
		{"terminal", tools.NewTerminalCallView("npm run build"), "terminal"},
		{"diff", tools.NewDiffCallView("Write foo.txt", []tools.FileDiff{{Path: "foo.txt", NewText: "hi"}}), "diff"},
	}
	for _, c := range cases {
		if got := tools.CardOf(c.view); got != c.want {
			t.Errorf("%s: CardOf = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestToolPresentationAllResultCards verifies result-card families, including search shapes and
// web kinds.
func TestToolPresentationAllResultCards(t *testing.T) {
	cases := []struct {
		name string
		view tools.ToolResultView
		want string
	}{
		{"generic", tools.NewGenericResultView(), "generic"},
		{"terminal", tools.NewTerminalResultView("output"), "terminal"},
		{"diff", &tools.DiffResultView{Card: "diff", Diffs: []tools.FileDiff{{Path: "a", NewText: "b"}}}, "diff"},
		{"search-matches", tools.NewSearchMatchesView(nil, false, 1), "search"},
		{"search-paths", tools.NewSearchPathsView([]string{"a.go"}, false, 1), "search"},
		{"read", tools.NewReadResultView("/x/a.go", 1, nil, 2), "read"},
		{"web-search", tools.NewWebSearchResultView(nil, "a", false), "web"},
		{"web-fetch", tools.NewWebFetchResultView("https://example.com", 200, false), "web"},
	}
	for _, c := range cases {
		if got := tools.CardOf(c.view); got != c.want {
			t.Errorf("%s: CardOf = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestToolPresentationSearchShapes verifies both search-result shapes.
func TestToolPresentationSearchShapes(t *testing.T) {
	m := tools.NewSearchMatchesView([]tools.SearchFileMatches{
		{Path: "a.go", Matches: []tools.SearchLineMatch{{LineNumber: 1, Line: "func main()"}}},
	}, true, 100)
	if m.Card != "search" || m.Shape != "matches" || !m.Truncated || m.Total != 100 {
		t.Fatalf("search-matches 字段错误: %+v", m)
	}
	p := tools.NewSearchPathsView([]string{"a.go", "b.go"}, false, 2)
	if p.Shape != "paths" || len(p.Paths) != 2 || p.Truncated {
		t.Fatalf("search-paths 字段错误: %+v", p)
	}
}

// TestToolPresentationWebKinds verifies both web-result kinds.
func TestToolPresentationWebKinds(t *testing.T) {
	s := tools.NewWebSearchResultView([]tools.WebSource{{URL: "https://x", Title: "X"}}, "answer", false)
	if s.Kind != "search" || len(s.Sources) != 1 || s.Answer != "answer" {
		t.Fatalf("web-search 字段错误: %+v", s)
	}
	f := tools.NewWebFetchResultView("https://y", 200, false)
	if f.Kind != "fetch" || f.URL != "https://y" || f.StatusCode != 200 {
		t.Fatalf("web-fetch 字段错误: %+v", f)
	}
}

// TestToolPresentationCustomCard permits custom types beyond the built-in vocabulary.
func TestToolPresentationCustomCard(t *testing.T) {
	var view any = customCard{Card: "mycard", Note: "custom"}
	if got := tools.CardOf(view); got != "other" {
		t.Fatalf("自定义卡片 CardOf 应回落 other, 实际 %s", got)
	}
}
