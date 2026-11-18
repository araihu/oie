package sqlite

import (
	"context"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

// plainReactionPage resolves the common empty snapshots together. Both queries
// use the caller's read transaction, so an observation cannot appear between
// testing for evidence and collecting the references used for eligibility.
func plainReactionPage(ctx context.Context, q *dbgen.Queries, c domain.Conversation, ids []domain.ID) (map[domain.ID]*domain.Message, error) {
	rows, err := q.ListPlainReactionSources(ctx, dbgen.ListPlainReactionSourcesParams{ConversationID: string(c.ID), Address: c.Address, MessageIds: jsonText(ids)})
	if err != nil {
		return nil, err
	}
	plain := make(map[domain.ID]*domain.Message, len(rows))
	sources := make([]domain.ID, 0, len(rows))
	for _, row := range rows {
		m, err := decodeMessage(row)
		if err != nil {
			return nil, err
		}
		if string(m.ID) != row.ID || m.ConversationID != c.ID || m.AccountID != c.AccountID || m.Moderation != nil || m.Retraction != nil {
			continue
		}
		m.References = nil
		plain[m.ID] = &m
		sources = append(sources, m.ID)
	}
	if len(sources) == 0 {
		return plain, nil
	}
	refs, err := q.ListPlainReactionReferences(ctx, jsonText(sources))
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		m := plain[domain.ID(r.MessageID)]
		if len(m.References) < maxMessageReferences && m.SourceBodyHash() == r.SourceHash {
			appendMessageReference(m, domain.MessageReference{ID: r.RemoteID, Scope: r.Scope, Author: r.Author})
		}
	}
	return plain, nil
}
