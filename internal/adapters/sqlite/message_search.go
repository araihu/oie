package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) SearchMessages(ctx context.Context, query domain.MessageSearchQuery) (domain.MessageSearchPage, error) {
	page := domain.MessageSearchPage{Query: query.Search, AccountID: query.AccountID, ConversationID: query.ConversationID, IncludeEditHistory: query.IncludeEditHistory, Items: []domain.MessageSearchHit{}}
	cursor, err := application.ParseMessageSearchCursor(query.Cursor, query.ScopeHash)
	if err != nil {
		return page, err
	}
	tx, q, err := begin(ctx, s)
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	history := int64(0)
	if query.IncludeEditHistory {
		history = 1
	}
	rows, err := q.SearchMessages(ctx, dbgen.SearchMessagesParams{AccountID: string(query.AccountID), ConversationID: string(query.ConversationID), IncludeHistory: history, Search: query.Search, ParserVersion: application.SharedContentVersion, PageLimit: int64(query.Limit + 1), CursorID: string(cursor.ID), CursorMs: cursor.Milliseconds, CursorCurrent: cursor.Current, CursorRevisionAt: cursor.RevisionAt, CursorRevisionID: string(cursor.RevisionID)})
	if err != nil {
		return page, err
	}
	more := len(rows) > query.Limit
	if more {
		rows = rows[:query.Limit]
	}
	for _, row := range rows {
		hit := domain.MessageSearchHit{AccountLabel: row.AccountLabel, MatchedRevisionID: domain.ID(row.MatchedRevisionID), Historical: row.IsCurrent == 0, MatchKind: "text"}
		if err = json.Unmarshal([]byte(row.Payload), &hit.Message); err != nil {
			return page, err
		}
		setTimelineTime(&hit.Message, row.TimelineAtMs)
		var c domain.Conversation
		if err = json.Unmarshal([]byte(row.ConversationPayload), &c); err != nil {
			return page, err
		}
		hit.ConversationTitle = c.Title
		if err = hydrateMessageProjection(ctx, q, &hit.Message); err != nil {
			return page, err
		}
		if err = hydrateReply(ctx, q, &hit.Message); err != nil {
			return page, err
		}
		hit.MatchedAt, err = time.Parse(time.RFC3339Nano, row.MatchedAt)
		if err != nil {
			return page, err
		}
		content := row.Body
		if row.TextMatch == 0 {
			hit.MatchKind = "metadata"
			content = row.Metadata
			if !matchSharedMetadata(content, query.Search) {
				content = row.Body + " " + content
			}
		}
		hit.Excerpt = messageSearchExcerpt(content, query.Search)
		page.Items = append(page.Items, hit)
	}
	if more {
		last := rows[len(rows)-1]
		page.NextCursor = application.EncodeMessageSearchCursor(application.MessageSearchCursor{Scope: query.ScopeHash, Milliseconds: last.PositionMs, ID: domain.ID(last.ID), Current: last.IsCurrent, RevisionAt: last.RevisionKeyAt, RevisionID: domain.ID(last.RevisionKeyID)})
	}
	status, err := q.SharedContentIndexStatus(ctx, dbgen.SharedContentIndexStatusParams{ParserVersion: application.SharedContentVersion, AccountID: string(query.AccountID), ConversationID: string(query.ConversationID), MessageIds: "[]"})
	if err != nil {
		return page, err
	}
	state := "complete"
	if status.IndexedMessages < status.TotalMessages {
		state = "pending"
	}
	page.MetadataIndexing = domain.SharedContentIndex{State: state, IndexedMessages: status.IndexedMessages, TotalMessages: status.TotalMessages}
	return page, tx.Commit()
}

// Excerpts preserve source text. The query only selects a bounded window; it never
// contributes markup, highlighting, or replacement characters to retained content.
func messageSearchExcerpt(content, query string) string {
	runes := []rune(content)
	if len(runes) <= 512 {
		return content
	}
	start := 0
	terms := strings.Fields(query)
	for i := 0; i < len(runes); {
		end := i + 1
		for end < len(runes) && !unicode.IsSpace(runes[end]) {
			end++
		}
		word := string(runes[i:end])
		found := false
		for _, term := range terms {
			if matchSharedMetadata(word, term) {
				found = true
				break
			}
		}
		if found {
			start = max(0, i-160)
			break
		}
		i = end + 1
	}
	if start+512 > len(runes) {
		start = max(0, len(runes)-512)
	}
	return string(runes[start:min(start+512, len(runes))])
}
