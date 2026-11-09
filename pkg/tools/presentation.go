// Tool presentation provides a neutral vocabulary matching upstream presentation.ts (M47). Tools
// declare call and result cards; UIs dispatch on card type, never on tool name. Calls support
// generic, terminal, and diff cards; results support generic, terminal, diff, search, read, and
// web cards. String discriminators permit custom cards.
package tools

// Tool-call categories.

// ToolCallKind selects a UI icon or style; other is the default.
type ToolCallKind string

// Supported tool-call categories.
const (
	CallKindRead    ToolCallKind = "read"
	CallKindEdit    ToolCallKind = "edit"
	CallKindDelete  ToolCallKind = "delete"
	CallKindMove    ToolCallKind = "move"
	CallKindSearch  ToolCallKind = "search"
	CallKindExecute ToolCallKind = "execute"
	CallKindFetch   ToolCallKind = "fetch"
	CallKindOther   ToolCallKind = "other"
)

// File locations and diffs shared by call and result views.

// FileLocation identifies a file position for editor navigation.
type FileLocation struct {
	// Path is the model-facing path operated on by the tool.
	Path string `json:"path"`
	// Line is an optional one-based focus line.
	Line int `json:"line,omitempty"`
}

// FileDiff describes a proposed single-file change for inline rendering.
type FileDiff struct {
	// Path is the file path.
	Path string `json:"path"`
	// OldText is the previous content, or nil when no prior content is available for a new file or
	// overwrite.
	OldText *string `json:"oldText,omitempty"`
	// NewText is the resulting content.
	NewText string `json:"newText"`
}

// ToolCallView variants: generic, terminal, and diff.

// ToolCallView describes a pending invocation using a card discriminator.
type ToolCallView interface {
	isCallView()
}

// GenericCallView provides a title with optional category, highlighted input, extra content, and
// location.
type GenericCallView struct {
	Card      string         `json:"card"`
	Title     string         `json:"title"`
	Kind      ToolCallKind   `json:"kind,omitempty"`
	RawInput  any            `json:"rawInput,omitempty"`
	Content   []any          `json:"content,omitempty"`
	Locations []FileLocation `json:"locations,omitempty"`
}

func (GenericCallView) isCallView() {}

// NewGenericCallView creates a generic invocation card.
func NewGenericCallView(title string) *GenericCallView {
	return &GenericCallView{Card: "generic", Title: title, Kind: CallKindOther}
}

// TerminalCallView displays a terminal command with its working directory as the header.
type TerminalCallView struct {
	Card        string `json:"card"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
}

func (TerminalCallView) isCallView() {}

// NewTerminalCallView creates a terminal invocation card.
func NewTerminalCallView(command string) *TerminalCallView {
	return &TerminalCallView{Card: "terminal", Title: command}
}

// DiffCallView displays an inline diff for file creation or modification.
type DiffCallView struct {
	Card      string         `json:"card"`
	Title     string         `json:"title"`
	Diffs     []FileDiff     `json:"diffs"`
	Locations []FileLocation `json:"locations,omitempty"`
}

func (DiffCallView) isCallView() {}

// NewDiffCallView creates a diff invocation card.
func NewDiffCallView(title string, diffs []FileDiff) *DiffCallView {
	return &DiffCallView{Card: "diff", Title: title, Diffs: diffs}
}

// ToolResultView variants: generic, terminal, diff, search, read, and web.

// ToolResultView describes a completed invocation using a card discriminator. Search supports
// matches and paths; web supports search and fetch variants.
type ToolResultView interface {
	isResultView()
}

// GenericResultView is the default completion card.
type GenericResultView struct {
	Card    string `json:"card"`
	Title   string `json:"title,omitempty"`
	Content []any  `json:"content,omitempty"`
}

func (GenericResultView) isResultView() {}

// NewGenericResultView creates a generic result card.
func NewGenericResultView() *GenericResultView {
	return &GenericResultView{Card: "generic"}
}

// TerminalResultView describes terminal-command completion.
type TerminalResultView struct {
	Card     string `json:"card"`
	Title    string `json:"title,omitempty"`
	Output   string `json:"output,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

func (TerminalResultView) isResultView() {}

// NewTerminalResultView creates a terminal result card.
func NewTerminalResultView(output string) *TerminalResultView {
	return &TerminalResultView{Card: "terminal", Output: output}
}

// DiffResultView displays a completed file change.
type DiffResultView struct {
	Card  string     `json:"card"`
	Title string     `json:"title,omitempty"`
	Diffs []FileDiff `json:"diffs"`
}

func (DiffResultView) isResultView() {}

// SearchLineMatch describes one matching line in a file.
type SearchLineMatch struct {
	LineNumber int    `json:"lineNumber"`
	Line       string `json:"line"`
}

// SearchFileMatches groups matches by file.
type SearchFileMatches struct {
	Path    string            `json:"path"`
	Matches []SearchLineMatch `json:"matches"`
}

// SearchResultView displays matches or matching paths.
type SearchResultView struct {
	Card      string              `json:"card"`
	Shape     string              `json:"shape"` // "matches" | "paths"
	Title     string              `json:"title,omitempty"`
	Files     []SearchFileMatches `json:"files,omitempty"`
	Paths     []string            `json:"paths,omitempty"`
	Truncated bool                `json:"truncated"`
	Total     int                 `json:"total"`
}

func (SearchResultView) isResultView() {}

// NewSearchMatchesView creates a result card with matches grouped by file.
func NewSearchMatchesView(files []SearchFileMatches, truncated bool, total int) *SearchResultView {
	return &SearchResultView{Card: "search", Shape: "matches", Files: files, Truncated: truncated, Total: total}
}

// NewSearchPathsView creates a result card containing a flat path list.
func NewSearchPathsView(paths []string, truncated bool, total int) *SearchResultView {
	return &SearchResultView{Card: "search", Shape: "paths", Paths: paths, Truncated: truncated, Total: total}
}

// ReadFileLine contains one file line and its one-based line number.
type ReadFileLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// ReadResultView displays file content with line numbers.
type ReadResultView struct {
	Card       string         `json:"card"`
	Title      string         `json:"title,omitempty"`
	Path       string         `json:"path"`
	Offset     int            `json:"offset"`
	Lines      []ReadFileLine `json:"lines"`
	TotalLines int            `json:"totalLines"`
	Lang       string         `json:"lang,omitempty"`
	Content    []any          `json:"content,omitempty"`
}

func (ReadResultView) isResultView() {}

// NewReadResultView creates a file-read result card.
func NewReadResultView(path string, offset int, lines []ReadFileLine, totalLines int) *ReadResultView {
	return &ReadResultView{Card: "read", Path: path, Offset: offset, Lines: lines, TotalLines: totalLines}
}

// WebSource identifies a citable web-search source.
type WebSource struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
}

// WebResultView displays completed web search or fetch results.
type WebResultView struct {
	Card       string      `json:"card"`
	Kind       string      `json:"kind"` // "search" | "fetch"
	Title      string      `json:"title,omitempty"`
	Sources    []WebSource `json:"sources,omitempty"`
	Answer     string      `json:"answer,omitempty"`
	URL        string      `json:"url,omitempty"`
	StatusCode int         `json:"statusCode,omitempty"`
	Truncated  bool        `json:"truncated"`
}

func (WebResultView) isResultView() {}

// NewWebSearchResultView creates a web-search result card.
func NewWebSearchResultView(sources []WebSource, answer string, truncated bool) *WebResultView {
	return &WebResultView{Card: "web", Kind: "search", Sources: sources, Answer: answer, Truncated: truncated}
}

// NewWebFetchResultView creates a web-fetch result card.
func NewWebFetchResultView(url string, statusCode int, truncated bool) *WebResultView {
	return &WebResultView{Card: "web", Kind: "fetch", URL: url, StatusCode: statusCode, Truncated: truncated}
}

// Card discriminator helpers and custom-card support.

// CardOf returns the discriminator of a call or result card; custom types can expose the same
// field.
func CardOf(v any) string {
	switch tv := v.(type) {
	case ToolCallView:
		// Extract the declared card field consistently.
		switch tv.(type) {
		case *GenericCallView:
			return "generic"
		case *TerminalCallView:
			return "terminal"
		case *DiffCallView:
			return "diff"
		case GenericCallView:
			return "generic"
		case TerminalCallView:
			return "terminal"
		case DiffCallView:
			return "diff"
		}
	case ToolResultView:
		switch r := tv.(type) {
		case *GenericResultView:
			return "generic"
		case *TerminalResultView:
			return "terminal"
		case *DiffResultView:
			return "diff"
		case SearchResultView:
			return "search"
		case *SearchResultView:
			return "search"
		case ReadResultView:
			return "read"
		case *ReadResultView:
			return "read"
		case WebResultView:
			return "web"
		case *WebResultView:
			return "web"
		case GenericResultView:
			return "generic"
		case TerminalResultView:
			return "terminal"
		case DiffResultView:
			return "diff"
		default:
			_ = r // Fall back to other for unrecognized custom result types.
		}
	}
	return "other"
}
