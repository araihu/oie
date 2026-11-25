package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"slices"
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
	observed, err := hasOwnTransmissionEvidence(ctx, q, m)
	if err != nil {
		return m, err
	}
	if observed {
		m, err = updateMessage(ctx, q, m, "unknown", revision, at, at, false)
		if err != nil {
			return m, err
		}
		if err := tx.Commit(); err != nil {
			return m, err
		}
		return m, application.StateConflict{Current: m}
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
	if err := projectDispatchReplyReference(ctx, q, &m); err != nil {
		return m, err
	}
	return m, tx.Commit()
}

// Keep the approved reply plan and quote. The wire hint can only identify a
// currently verified reference to the same scoped target.
func projectDispatchReplyReference(ctx context.Context, q *dbgen.Queries, m *domain.Message) error {
	plan := m.OutboundReply
	if plan == nil || plan.Reference == nil {
		return nil
	}
	if m.Reply != nil && m.Reply.Available && !m.Reply.OriginalExpired {
		target := domain.Message{ID: m.Reply.MessageID}
		if err := hydrateReferences(ctx, q, &target); err != nil {
			return err
		}
		if slices.Contains(target.References, *plan.Reference) {
			return nil
		}
	}
	projected := *plan
	projected.Reference = nil
	m.OutboundReply = &projected
	return nil
}

// RecoverInterruptedMessage requeues only claims with durable evidence that
// the adapter never reached the wire. Protected pre-wire claims without
// attachments can reopen their mutation cutoff after an interruption.
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
	release := m.DirectEncryption == "omemo" && len(m.Attachments) == 0
	if _, err = updateMessageWithClaimRelease(ctx, q, m, state, revision, at, at, false, release); err != nil {
		return err
	}
	return tx.Commit()
}
