package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

type restoreFact struct {
	message string
	proof   domain.MessageCorrection
}

func backupCorrectionFacts(ctx context.Context, tx *sql.Tx) ([]restoreFact, error) {
	rows, err := tx.QueryContext(ctx, restoreScript("facts"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var facts []restoreFact
	for rows.Next() {
		var fact restoreFact
		if err := rows.Scan(&fact.message, &fact.proof.Scope, &fact.proof.Author, &fact.proof.ID, &fact.proof.TargetID); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func restoreDerivedHistory(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, facts []restoreFact, now time.Time) error {
	if _, err := tx.ExecContext(ctx, restoreScript("exclusions")); err != nil {
		return err
	}
	for _, fact := range facts {
		row, err := q.GetMessage(ctx, fact.message)
		if err != nil {
			return err
		}
		seed, err := decodeMessage(row)
		if err != nil {
			return err
		}
		if err := reconcileCorrections(ctx, q, seed, &fact.proof); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, restoreScript("summary"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var added bool
		if err := rows.Scan(&id, &added); err != nil {
			return err
		}
		if err := scrubRestoredQuotes(ctx, tx, q, id); err != nil {
			return err
		}
		if err := refreshRestoredConversation(ctx, q, id, added, now); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scrubRestoredQuotes(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, id string) error {
	rows, err := tx.QueryContext(ctx, restoreScript("excluded_ids"), id)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var excluded []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return err
		}
		excluded = append(excluded, source)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(excluded) > 0 {
		ids := jsonText(excluded)
		if err := q.ScrubMessageOutboundQuotes(ctx, dbgen.ScrubMessageOutboundQuotesParams{ConversationID: id, ExpiredIds: ids}); err != nil {
			return err
		}
		if err := q.ScrubRevisionOutboundQuotes(ctx, dbgen.ScrubRevisionOutboundQuotesParams{OwnerID: id, ExpiredIds: ids}); err != nil {
			return err
		}
		if err := q.ScrubReceiptOutboundQuotes(ctx, dbgen.ScrubReceiptOutboundQuotesParams{OwnerID: id, ExpiredIds: ids}); err != nil {
			return err
		}
	}
	_, err = scrubInboundQuotes(ctx, q, id)
	return err
}

func refreshRestoredConversation(ctx context.Context, q *dbgen.Queries, id string, added bool, now time.Time) error {
	if err := refreshConversationSummary(ctx, q, domain.ID(id), now); err != nil {
		return err
	}
	row, err := q.GetConversation(ctx, id)
	if err != nil {
		return err
	}
	c, err := decodeConversation(row)
	if err != nil {
		return err
	}
	read, err := q.ConversationUnreadSummary(ctx, id)
	if err != nil {
		return err
	}
	if c.UnreadCount != int(read.UnreadCount) || c.UnreadMention != (read.UnreadMention != 0) {
		c.UnreadCount, c.UnreadMention = int(read.UnreadCount), read.UnreadMention != 0
		if err := q.UpdateConversation(ctx, dbgen.UpdateConversationParams{ID: id, RevisionID: row.RevisionID, Payload: jsonText(c), ActivitySequence: row.ActivitySequence, UnreadCount: read.UnreadCount}); err != nil {
			return err
		}
		_, err = change(ctx, q, "conversation.updated", c, now)
		return err
	}
	if added {
		_, err = change(ctx, q, "conversation.created", c, now)
	}
	return err
}
