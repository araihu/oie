package sqlite

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/diagnostics"
	"github.com/blevesearch/bleve/v2/search/query"
)

const searchCandidateTimeout = 25 * time.Millisecond

func (i *messageIndex) selectRows(ctx context.Context, _ *sql.Tx, q *dbgen.Queries, p dbgen.SearchMessagesParams) ([]dbgen.SearchMessagesRow, error) {
	candidateQuery, err := messageIndexQuery(p)
	if err != nil {
		return nil, err
	}
	if candidateQuery == nil {
		return searchChronological(ctx, q, p)
	}
	ids, ok := i.candidates(ctx, q, p, candidateQuery)
	if !ok {
		return searchChronological(ctx, q, p)
	}
	if len(ids) == 0 {
		i.used.Add(1)
		return nil, nil
	}
	verified, err := q.VerifyIndexedMessages(ctx, dbgen.VerifyIndexedMessagesParams{
		MessageIds: jsonText(ids),
		CursorID:   p.CursorID, CursorMs: p.CursorMs, CursorCurrent: p.CursorCurrent,
		CursorRevisionAt: p.CursorRevisionAt, CursorRevisionID: p.CursorRevisionID, PageLimit: p.PageLimit,
		ParserVersion: p.ParserVersion, AccountID: p.AccountID, AccountIds: p.AccountIds,
		ConversationID: p.ConversationID, IncludeHistory: p.IncludeHistory, Search: p.Search,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		i.store.diagnostics.Record(diagnostics.IndexUnavailable, 0, 0)
		return searchChronological(ctx, q, p)
	}
	rows := make([]dbgen.SearchMessagesRow, len(verified))
	for j, row := range verified {
		rows[j] = dbgen.SearchMessagesRow(row)
	}
	i.used.Add(1)
	return rows, nil
}

func (i *messageIndex) candidates(ctx context.Context, q *dbgen.Queries, p dbgen.SearchMessagesParams, candidateQuery query.Query) ([]string, bool) {
	i.gate.RLock()
	generation, epoch := i.active, i.epoch
	i.gate.RUnlock()
	if generation == nil {
		return nil, false
	}
	if p.ConversationID != "" {
		count, err := q.CountMessageSearchRoomPrefix(ctx, dbgen.CountMessageSearchRoomPrefixParams{
			ConversationID: p.ConversationID, MaxCount: searchSmallRoom + 1,
		})
		if err != nil || count <= searchSmallRoom {
			return nil, false
		}
	}
	// The first SELECT (the room count when scoped) pins the WAL snapshot.
	// Pending work in that snapshot covers stale or absent postings from a
	// batch that was already in progress when the epoch was captured.
	var pending []string
	var err error
	if p.ConversationID != "" || p.AccountID != "" || p.AccountIds != "[]" {
		pending, err = q.ListScopedMessageSearchUnindexed(ctx, dbgen.ListScopedMessageSearchUnindexedParams{
			ConversationID: p.ConversationID, AccountID: p.AccountID, AccountIds: p.AccountIds, MaxIds: searchCandidateLimit + 1,
		})
	} else {
		pending, err = q.ListMessageSearchUnindexed(ctx, searchCandidateLimit+1)
	}
	if err != nil || len(pending) > searchCandidateLimit {
		return nil, false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, searchCandidateTimeout)
	defer cancel()
	// Bleve synchronizes SearchInContext with Close. A retired generation
	// captured before this lookup either allows it to finish or returns an error.
	candidates, ok := lookupMessageIndex(lookupCtx, generation.index, candidateQuery, searchCandidateLimit-len(pending))
	if !ok {
		i.lookupFallback.Add(1)
		return nil, false
	}
	i.gate.RLock()
	unchanged := i.active == generation && i.epoch == epoch
	i.gate.RUnlock()
	if !unchanged {
		i.crossedBatch.Add(1)
		return nil, false
	}
	return append(candidates, pending...), true
}

func searchChronological(ctx context.Context, q *dbgen.Queries, p dbgen.SearchMessagesParams) ([]dbgen.SearchMessagesRow, error) {
	if p.IncludeHistory != 0 {
		return q.SearchMessages(ctx, p)
	}
	if p.CursorID == "" {
		p.CursorID, p.CursorMs = "~", math.MaxInt64
	}
	params := dbgen.SearchMessagesChronologicalParams{
		Search: p.Search, ParserVersion: p.ParserVersion, AccountID: p.AccountID, AccountIds: p.AccountIds,
		ConversationID: p.ConversationID, CursorMs: &p.CursorMs, CursorID: p.CursorID, CursorCurrent: p.CursorCurrent,
		CursorRevisionAt: p.CursorRevisionAt, CursorRevisionID: p.CursorRevisionID, PageLimit: p.PageLimit,
	}
	if p.ConversationID != "" {
		found, err := q.SearchRoomMessagesChronological(ctx, dbgen.SearchRoomMessagesChronologicalParams(params))
		rows := make([]dbgen.SearchMessagesRow, len(found))
		for j, row := range found {
			rows[j] = dbgen.SearchMessagesRow(row)
		}
		return rows, err
	}
	found, err := q.SearchMessagesChronological(ctx, params)
	rows := make([]dbgen.SearchMessagesRow, len(found))
	for j, row := range found {
		rows[j] = dbgen.SearchMessagesRow(row)
	}
	return rows, err
}
