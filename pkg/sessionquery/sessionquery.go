// Package sessionquery queries sessions and FTS5 search (S04), matching storage/session-query.
// It reads the S03 SQLite store through ListSessions, Load, and Search. Titles use
// the latest title event or the first user-message prefix. ListSummaries filters
// by title prefix, creation-time range, and limit, ordered by most recent update.
// Search unions FTS5 body/title hits with title-substring matches and returns summaries.
package sessionquery

import (
	"context"
	"strings"
	"time"

	"github.com/herlanai/omnix/pkg/brand"
	sqlstore "github.com/herlanai/omnix/pkg/persistence/sqlite"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sessiontitle"
)

// Types.

// SessionSummary is a queryable session summary.
type SessionSummary struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	EventCount int       `json:"eventCount"`
}

// ListRequest configures the three session-list filters.
type ListRequest struct {
	TitlePrefix string     // Optional title-prefix filter.
	CreatedFrom *time.Time // Optional inclusive lower creation-time bound.
	CreatedTo   *time.Time // Optional inclusive upper creation-time bound.
	Limit       int        // Maximum results; nonpositive means unlimited.
}

// SearchRequest configures full-text search.
type SearchRequest struct {
	Keyword string // Nonempty search keyword.
	Limit   int    // Maximum results.
}

// QueryService finds sessions by title, time, or keyword.
type QueryService struct {
	store *sqlstore.Store
}

// New binds a query service to the S03 SQLite session store.
func New(store *sqlstore.Store) *QueryService {
	return &QueryService{store: store}
}

// Session summary derivation.

// deriveSummary derives title, timestamps, and counts from all session events.
func deriveSummary(id string, events []session.SessionEvent) SessionSummary {
	sum := SessionSummary{ID: id, EventCount: len(events)}
	if len(events) == 0 {
		return sum
	}
	sum.CreatedAt = events[0].Time
	sum.UpdatedAt = events[len(events)-1].Time

	// Use the latest title, falling back to the first user-message prefix.
	title := session.FoldSessionTitle(events).Title
	if title == "" {
		title = firstUserMessage(events)
	}
	sum.Title = title
	return sum
}

// firstUserMessage returns the first user message for title fallback.
func firstUserMessage(events []session.SessionEvent) string {
	for _, ev := range events {
		if ev.Type == session.EventUserMessage {
			if d, ok := ev.Data.(session.UserMessageData); ok {
				return sessiontitle.Fallback(d.Content)
			}
		}
	}
	return ""
}

// Listing with three filters.

// ListSummaries filters by title prefix and creation range, ordered by latest update first.
func (q *QueryService) ListSummaries(ctx context.Context, req ListRequest) ([]SessionSummary, error) {
	ids, err := q.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []SessionSummary
	for _, id := range ids {
		events, err := q.store.Load(ctx, brand.NewSessionID(id))
		if err != nil {
			return nil, err
		}
		sum := deriveSummary(id, events)
		// Filter by title prefix.
		if req.TitlePrefix != "" && !strings.HasPrefix(sum.Title, req.TitlePrefix) {
			continue
		}
		// Filter by creation-time range.
		if req.CreatedFrom != nil && sum.CreatedAt.Before(*req.CreatedFrom) {
			continue
		}
		if req.CreatedTo != nil && sum.CreatedAt.After(*req.CreatedTo) {
			continue
		}
		out = append(out, sum)
	}
	// Sort updates newest first.
	sortByUpdatedDesc(out)
	if req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

// FTS5 search.

// Search returns summaries for the union of FTS5 content hits and title-substring matches.
func (q *QueryService) Search(ctx context.Context, req SearchRequest) ([]SessionSummary, error) {
	keyword := strings.TrimSpace(req.Keyword)
	if keyword == "" {
		return nil, nil
	}

	// Collect session IDs from FTS body/title matches.
	matches, err := q.store.Search(ctx, keyword)
	if err != nil {
		return nil, err
	}
	hitSessions := map[string]bool{}
	for _, m := range matches {
		hitSessions[m.SessionID] = true
	}

	// Include sessions whose titles contain the keyword even without other FTS hits.
	ids, err := q.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []SessionSummary
	for _, id := range ids {
		events, err := q.store.Load(ctx, brand.NewSessionID(id))
		if err != nil {
			return nil, err
		}
		sum := deriveSummary(id, events)
		if hitSessions[id] || strings.Contains(sum.Title, keyword) {
			out = append(out, sum)
		}
	}
	sortByUpdatedDesc(out)
	if req.Limit > 0 && len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

// Helpers.

// sortByUpdatedDesc orders summaries by most recent update first.
func sortByUpdatedDesc(list []SessionSummary) {
	// Insertion sort for typically small result lists.
	for i := 1; i < len(list); i++ {
		j := i
		for j > 0 && list[j].UpdatedAt.After(list[j-1].UpdatedAt) {
			list[j], list[j-1] = list[j-1], list[j]
			j--
		}
	}
}
