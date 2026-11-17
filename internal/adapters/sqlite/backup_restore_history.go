package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
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
	cutoffTime := sql.Named("now_ms", now.UnixMilli())
	if _, err := tx.ExecContext(ctx, restoreScript("exclusions"), cutoffTime); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, restoreScript("redacted_quotes"), cutoffTime); err != nil {
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
	if _, err := tx.ExecContext(ctx, restoreScript("reply_fallbacks")); err != nil {
		return err
	}
	if err := scrubRestoredQuotePreviews(ctx, tx, q); err != nil {
		return err
	}
	if err := publishRestoredAccounts(ctx, tx, q, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, restoreScript("summary"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var added, historyChanged, promoted, redacted bool
		if err := rows.Scan(&id, &added, &historyChanged, &promoted, &redacted); err != nil {
			return err
		}
		scrubbed, err := scrubRestoredQuotes(ctx, tx, q, id)
		if err != nil {
			return err
		}
		mentionsChanged, err := refreshRestoredMentions(ctx, tx, q, id)
		if err != nil {
			return err
		}
		if redacted || scrubbed {
			if err := q.ScrubConversationEventPreviews(ctx, id); err != nil {
				return err
			}
			if err := q.ScrubConversationRevisionPreviews(ctx, id); err != nil {
				return err
			}
			if err := q.ScrubConversationReceiptPreviews(ctx, id); err != nil {
				return err
			}
		}
		if err := refreshRestoredConversation(ctx, q, id, added, promoted, now); err != nil {
			return err
		}
		if !added && (historyChanged || scrubbed || redacted || mentionsChanged) {
			if _, err := change(ctx, q, "conversation.history_pruned", domain.HistoryPruned{
				ConversationID: domain.ID(id), MessageIDs: []domain.ID{},
			}, now); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func refreshRestoredMentions(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, id string) (bool, error) {
	rows, err := tx.QueryContext(ctx, restoreScript("mention_sources"), id)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	changed := false
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return false, err
		}
		row, err := q.GetMessage(ctx, sourceID)
		if err != nil {
			return false, err
		}
		source, err := decodeMessage(row)
		if err != nil {
			return false, err
		}
		updated, err := refreshMessageMention(ctx, q, &source)
		if err != nil {
			return false, err
		}
		changed = changed || updated
	}
	return changed, rows.Err()
}

func scrubRestoredQuotePreviews(ctx context.Context, tx *sql.Tx, q *dbgen.Queries) error {
	rows, err := tx.QueryContext(ctx, restoreScript("redacted_preview_bodies"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	removed := make(redactedQuoteURLs)
	for rows.Next() {
		var message, canonical domain.ID
		var before, after string
		if err := rows.Scan(&message, &canonical, &before, &after); err != nil {
			return err
		}
		removed.add(message, canonical, before, after)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE restore_redacted_quotes"); err != nil {
		return err
	}
	return removed.prune(ctx, q)
}

func publishRestoredAccounts(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, now time.Time) error {
	rows, err := tx.QueryContext(ctx, restoreScript("new_accounts"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var account domain.Account
		if err := json.Unmarshal([]byte(payload), &account); err != nil {
			return err
		}
		if _, err := change(ctx, q, "account.registered", account, now); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scrubRestoredQuotes(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, id string) (bool, error) {
	rows, err := tx.QueryContext(ctx, restoreScript("excluded_ids"), id)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	var excluded []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			return false, err
		}
		excluded = append(excluded, source)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(excluded) > 0 {
		ids := jsonText(excluded)
		if err := q.ScrubMessageOutboundQuotes(ctx, dbgen.ScrubMessageOutboundQuotesParams{ConversationID: id, ExpiredIds: ids}); err != nil {
			return false, err
		}
		if err := q.ScrubRevisionOutboundQuotes(ctx, dbgen.ScrubRevisionOutboundQuotesParams{OwnerID: id, ExpiredIds: ids}); err != nil {
			return false, err
		}
		if err := q.ScrubEventOutboundQuotes(ctx, dbgen.ScrubEventOutboundQuotesParams{OwnerID: id, ExpiredIds: ids}); err != nil {
			return false, err
		}
		if err := q.ScrubReceiptOutboundQuotes(ctx, dbgen.ScrubReceiptOutboundQuotesParams{OwnerID: id, ExpiredIds: ids}); err != nil {
			return false, err
		}
		if err := scrubRecoveredSendQuotes(ctx, q, id, ids); err != nil {
			return false, err
		}
	}
	return scrubInboundQuotes(ctx, q, id)
}

func refreshRestoredConversation(ctx context.Context, q *dbgen.Queries, id string, added, promoted bool, now time.Time) error {
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
	readChanged := c.UnreadCount != int(read.UnreadCount) || c.UnreadMention != (read.UnreadMention != 0)
	repairCursor := c.ActivitySequence <= 0
	if !added && !promoted && !repairCursor && !readChanged {
		return nil
	}
	c.UnreadCount, c.UnreadMention = int(read.UnreadCount), read.UnreadMention != 0
	kind := "conversation.updated"
	if added {
		kind = "conversation.created"
	}
	sequence, err := change(ctx, q, kind, c, now)
	if err != nil {
		return err
	}
	if added || repairCursor {
		c.ActivitySequence = sequence
	}
	return q.UpdateConversation(ctx, dbgen.UpdateConversationParams{
		ID: id, RevisionID: row.RevisionID, Payload: jsonText(c),
		ActivitySequence: c.ActivitySequence, UnreadCount: read.UnreadCount,
	})
}
