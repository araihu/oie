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

func (s *Store) PendingMessagesByAttachments(ctx context.Context, at time.Time, withAttachments bool, after application.PendingMessageCursor) ([]domain.Message, application.PendingMessageCursor, error) {
	const pageLimit = 100
	var next application.PendingMessageCursor
	rows, err := s.queries.PendingMessagesByAttachments(ctx, dbgen.PendingMessagesByAttachmentsParams{
		NextAttemptAt: stamp(at), WithAttachments: number(withAttachments),
		AfterTime: stamp(after.NextAttemptAt), AfterID: string(after.MessageID), PageLimit: pageLimit,
	})
	if err != nil {
		return nil, next, err
	}
	messages := make([]domain.Message, 0, len(rows))
	for _, row := range rows {
		message, err := decodeMessage(row.Message)
		if err != nil {
			return nil, next, err
		}
		messages = append(messages, message)
	}
	if len(rows) == pageLimit {
		last := rows[len(rows)-1]
		next.NextAttemptAt, err = time.Parse(time.RFC3339Nano, last.NextAttemptAt)
		next.MessageID = domain.ID(last.Message.ID)
	}
	return messages, next, err
}

func (s *Store) ClaimDispatchMessage(ctx context.Context, id, expected, roomIntent, revision domain.ID, at time.Time, guardWire bool) (domain.Message, error) {
	if expected == "" {
		return domain.Message{}, application.ErrInvalid
	}
	return s.claimDispatchMessage(ctx, id, expected, roomIntent, revision, at, false, guardWire)
}

func (s *Store) claimDispatchMessage(ctx context.Context, id, expected, roomIntent, revision domain.ID, at time.Time, roomOnly, guardWire bool) (domain.Message, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.Message{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return domain.Message{}, err
	}
	row, err := q.GetMessage(ctx, string(id))
	if err != nil {
		return domain.Message{}, notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return m, err
	}
	if !m.IsOwn || m.SendState != "queued" || (expected != "" && m.RevisionID != expected) {
		return m, application.StateConflict{Current: m}
	}
	c, err := q.GetConversation(ctx, string(m.ConversationID))
	if err != nil {
		return m, notFound(err)
	}
	conversation, err := decodeConversation(c)
	if err != nil {
		return m, err
	}
	if conversation.AccountID != m.AccountID || (roomOnly && conversation.Kind != "room") {
		return m, application.ErrInvalid
	}
	if conversation.Kind == "room" {
		intent, err := q.GetRoomIntent(ctx, string(m.ConversationID))
		if errors.Is(err, sql.ErrNoRows) {
			return m, application.StateConflict{Current: m}
		}
		if err != nil {
			return m, err
		}
		if intent.Active != 1 || intent.RevisionID != string(roomIntent) {
			return m, application.StateConflict{Current: m}
		}
	}
	m, err = updateMessage(ctx, q, m, "dispatching", revision, at, at, guardWire)
	if err != nil {
		return m, err
	}
	return m, tx.Commit()
}

// RecoverInterruptedMessage requeues only claims with durable evidence that
// the adapter never reached the wire. The original mutation cutoff stays closed.
func (s *Store) RecoverInterruptedMessage(ctx context.Context, id, expected, revision domain.ID, at time.Time) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	row, err := q.GetMessage(ctx, string(id))
	if err != nil {
		return notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return err
	}
	if !m.IsOwn || m.SendState != "dispatching" || m.RevisionID != expected {
		return application.StateConflict{Current: m}
	}
	outbox, err := q.GetOutbox(ctx, string(id))
	if err != nil {
		return err
	}
	state := application.Unknown
	if outbox.State == "dispatching" && outbox.WireStage == "awaiting" {
		state = "queued"
	}
	if _, err = updateMessage(ctx, q, m, state, revision, at, at, false); err != nil {
		return err
	}
	return tx.Commit()
}
