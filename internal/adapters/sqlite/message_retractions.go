package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) RecordMessageRetraction(ctx context.Context, account domain.ID, kind, address string, in application.IncomingRetraction) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := q.InsertMessageRetraction(ctx, dbgen.InsertMessageRetractionParams{
		AccountID: string(account), Kind: kind, Address: address, Scope: in.Scope, RemoteID: in.RemoteID, RetractedAt: stamp(in.At),
	})
	if err != nil || n == 0 {
		return err
	}
	sources, err := retractionSources(ctx, q, account, kind, address, in)
	if err != nil {
		return err
	}
	for id := range sources {
		row, err := q.GetMessage(ctx, string(id))
		if err != nil {
			return err
		}
		message, err := decodeMessage(row)
		if err != nil {
			return err
		}
		if _, err = emitCanonicalMessageEvent(ctx, q, "message.updated", message, in.At); err != nil {
			return err
		}
		if err = refreshConversationSummary(ctx, q, message.ConversationID, in.At); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func retractionSources(ctx context.Context, q *dbgen.Queries, account domain.ID, kind, address string, in application.IncomingRetraction) (map[domain.ID]bool, error) {
	rows, err := q.MessageRetractionSources(ctx, dbgen.MessageRetractionSourcesParams{
		AccountID: string(account), Kind: kind, Address: address, Scope: in.Scope, RemoteID: in.RemoteID,
	})
	if err != nil {
		return nil, err
	}
	sources := make(map[domain.ID]bool, len(rows))
	for _, row := range rows {
		id, err := resolveMessageAlias(ctx, q, domain.ID(row))
		if err != nil {
			return nil, err
		}
		sources[id] = true
	}
	return sources, nil
}

func (s *Store) MatchesMessageRetraction(ctx context.Context, m domain.Message, c domain.Conversation, r application.IncomingRetraction) (bool, error) {
	sources, err := retractionSources(ctx, s.read, m.AccountID, c.Kind, c.Address, r)
	return len(sources) == 1 && sources[m.ID], err
}

func hydrateMessageWithdrawal(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	if err := hydrateRoomModeration(ctx, q, m); err != nil {
		return err
	}
	if m.Retraction != nil {
		return nil
	}
	var at time.Time
	if m.Moderation != nil {
		at = m.Moderation.At
	} else {
		value, err := q.GetMessageRetraction(ctx, string(m.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		at, err = time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return err
		}
	}
	m.Retraction = &domain.MessageRetraction{At: at, OriginalAvailable: m.OriginalText != "" || len(m.Attachments) != 0 || len(m.FileReferences) != 0 || len(m.AudioURLs) != 0}
	return nil
}

func (s *Store) RetractedOriginal(ctx context.Context, id domain.ID) (domain.Message, error) {
	tx, q, err := beginRead(ctx, s)
	if err != nil {
		return domain.Message{}, err
	}
	defer func() { _ = tx.Rollback() }()
	canonical, err := resolveMessageAlias(ctx, q, id)
	if err != nil {
		return domain.Message{}, err
	}
	row, err := q.GetVisibleMessage(ctx, string(canonical))
	if err != nil {
		return domain.Message{}, notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return m, err
	}
	if err = hydrateMessageContent(ctx, q, &m); err != nil {
		return m, err
	}
	if err = hydrateReply(ctx, q, &m); err != nil {
		return m, err
	}
	if err = hydrateMessageWithdrawal(ctx, q, &m); err != nil {
		return m, err
	}
	if m.Retraction == nil || !m.Retraction.OriginalAvailable {
		return domain.Message{}, application.ErrNotFound
	}
	return m, tx.Commit()
}
