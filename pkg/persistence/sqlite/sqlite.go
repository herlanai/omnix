// Package sqlite implements SQLite session persistence and FTS5 indexing (S03),
// following upstream storage/sqlite-session. session_events stores JSON blobs
// keyed by (session_id, seq); session_events_fts indexes message and title text
// for S04 queries. Append and AppendBatch update both tables transactionally.
// User/assistant Content and session Title supply index text; other events
// contribute empty text.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver.

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/session"
)

// ============================================================================
// DDL
// ============================================================================

const (
	ddlEvents = `
		CREATE TABLE IF NOT EXISTS session_events (
			session_id   TEXT NOT NULL,
			seq          INTEGER NOT NULL,
			event_type   TEXT NOT NULL,
			ts           DATETIME NOT NULL,
			data         BLOB NOT NULL,
			text_content TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (session_id, seq)
		);
	`
	ddlFTS = `
		CREATE VIRTUAL TABLE IF NOT EXISTS session_events_fts
			USING fts5(session_id UNINDEXED, seq UNINDEXED, text_content,
				tokenize = 'trigram');
	`
)

// ============================================================================
// Store
// ============================================================================

// Store persists session events in SQLite with an FTS5 index.
type Store struct {
	db *sql.DB
	// fts records FTS5 availability; search falls back to LIKE when unavailable.
	fts bool
}

// Open opens or creates the database and initializes tables and FTS5.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(ddlEvents); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("persistence/sqlite: init events table: %w", err)
	}
	st := &Store{db: db}
	// Try creating the virtual table; failure disables FTS.
	if _, err := db.Exec(ddlFTS); err == nil {
		st.fts = true
	} else {
		// Silently fall back to LIKE when FTS5 is unavailable.
		st.fts = false
	}
	return st, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// FTSEnabled reports FTS5 availability for callers and tests.
func (s *Store) FTSEnabled() bool { return s.fts }

// FTSUnavailable indicates the current driver does not support FTS5.
func (s *Store) FTSUnavailable() bool { return !s.fts }

// Search-text extraction.

// extractText derives searchable text from an event.
func extractText(ev session.SessionEvent) string {
	switch d := ev.Data.(type) {
	case session.UserMessageData:
		return d.Content
	case session.AssistantMessageData:
		return d.Content
	case session.SessionTitleData:
		return d.Title
	default:
		return ""
	}
}

// Atomic writes.

// Append inserts an event and its index entry in one transaction.
func (s *Store) Append(ctx context.Context, sessionID brand.SessionID, ev session.SessionEvent) error {
	return s.AppendBatch(ctx, sessionID, []session.SessionEvent{ev})
}

// AppendBatch atomically inserts multiple events in one transaction.
func (s *Store) AppendBatch(ctx context.Context, sessionID brand.SessionID, events []session.SessionEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO session_events(session_id, seq, event_type, ts, data, text_content)
		VALUES(?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	var ftsStmt *sql.Stmt
	if s.fts {
		ftsStmt, err = tx.PrepareContext(ctx, `
			INSERT INTO session_events_fts(session_id, seq, text_content)
			VALUES(?, ?, ?)`)
		if err != nil {
			return err
		}
	}

	for _, ev := range events {
		raw, err := ev.MarshalJSON()
		if err != nil {
			return err
		}
		txt := extractText(ev)
		if _, err := stmt.ExecContext(ctx, sessionID.Raw(), ev.Seq, string(ev.Type), ev.Time, raw, txt); err != nil {
			return err
		}
		if s.fts {
			if _, err := ftsStmt.ExecContext(ctx, sessionID.Raw(), ev.Seq, txt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Reading.

// Load returns session events in ascending sequence order.
func (s *Store) Load(ctx context.Context, sessionID brand.SessionID) ([]session.SessionEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT data FROM session_events WHERE session_id = ? ORDER BY seq`, sessionID.Raw())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []session.SessionEvent
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var ev session.SessionEvent
		if err := ev.UnmarshalJSON(raw); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Count returns the session's event count.
func (s *Store) Count(ctx context.Context, sessionID brand.SessionID) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_events WHERE session_id = ?`, sessionID.Raw()).Scan(&n)
	return n, err
}

// ListSessions returns known session IDs in deterministic lexical order for queries.
func (s *Store) ListSessions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT session_id FROM session_events ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Searching.

// Match identifies a session, sequence, and matched text; results sort by session ID.
type Match struct {
	SessionID string `json:"sessionId"`
	Seq       uint64 `json:"seq"`
	Text      string `json:"text,omitempty"`
}

// Search uses FTS5 and returns matching session/sequence pairs. Trigram queries
// require at least three runes; shorter queries or unavailable FTS5 fall back
// to LIKE without an inverted index.
func (s *Store) Search(ctx context.Context, query string) ([]Match, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if !s.fts || len([]rune(query)) < 3 {
		return s.searchLike(ctx, query)
	}
	return s.searchFTS(ctx, query)
}

// searchFTS executes an FTS5 MATCH query.
func (s *Store) searchFTS(ctx context.Context, query string) ([]Match, error) {
	// Phrase queries preserve adjacent CJK character sequences without spaces and
	// also support whitespace-separated Latin terms. Empty queries are rejected above.
	term := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.session_id, f.seq, f.text_content
		FROM session_events_fts f
		WHERE session_events_fts MATCH ?
		ORDER BY f.session_id, f.seq`, term)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.SessionID, &m.Seq, &m.Text); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// searchLike scans text_content with LIKE when FTS5 is unavailable.
func (s *Store) searchLike(ctx context.Context, query string) ([]Match, error) {
	pattern := "%" + query + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, seq, text_content FROM session_events
		WHERE text_content LIKE ?
		ORDER BY session_id, seq`, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		if err := rows.Scan(&m.SessionID, &m.Seq, &m.Text); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
