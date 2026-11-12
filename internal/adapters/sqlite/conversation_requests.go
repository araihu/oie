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

func (s *Store) PendingDirectHistoryChecks(ctx context.Context, account domain.ID) ([]domain.Conversation, error) {
	rows, err := s.queries.PendingDirectHistoryChecks(ctx, string(account))
	if err != nil {
		return nil, err
	}
	out := make([]domain.Conversation, 0, len(rows))
	for _, row := range rows {
		c, err := decodeConversation(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) ResolveDirectHistoryCheck(ctx context.Context, id domain.ID, check domain.DirectHistoryCheck, prior bool, revision domain.ID, at time.Time) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	row, err := q.GetConversation(ctx, string(id))
	if err != nil {
		return notFound(err)
	}
	current, err := decodeConversation(row)
	if err != nil {
		return err
	}
	if current.Request != domain.MessageRequest || current.DirectHistoryCheck == nil || *current.DirectHistoryCheck != check {
		return nil
	}
	next := current
	next.DirectHistoryCheck = nil
	if prior && !next.Archived {
		next.Request = ""
	}
	if err = writeConversationDecision(ctx, q, current, next, revision, at); err != nil {
		return err
	}
	return tx.Commit()
}

func writeConversationDecision(ctx context.Context, q *dbgen.Queries, current, next domain.Conversation, revision domain.ID, at time.Time) error {
	next.RevisionID = revision
	count, err := q.UpdateConversationPreferences(ctx, dbgen.UpdateConversationPreferencesParams{ID: string(next.ID), AccountID: string(next.AccountID), ExpectedRevisionID: string(current.RevisionID), RevisionID: string(revision), Payload: jsonText(next), Archived: number(next.Archived), Favorite: number(next.Favorite)})
	if err != nil {
		return err
	}
	if count != 1 {
		return application.ConversationPreferencesConflict{Current: current}
	}
	if err = q.InsertConversationRevision(ctx, dbgen.InsertConversationRevisionParams{ID: string(revision), EntityID: string(next.ID), Parents: jsonText([]domain.ID{current.RevisionID}), Payload: jsonText(next), CreatedAt: stamp(at)}); err != nil {
		return err
	}
	_, err = change(ctx, q, "conversation.updated", next, at)
	return err
}

func (s *Store) StoreRoomInvitation(ctx context.Context, c domain.Conversation, password string) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	_, err = q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(c.AccountID), Address: c.Address})
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = insertConversation(ctx, q, c); err != nil {
		return err
	}
	if password != "" {
		if err = q.InsertRoomInvitationCredential(ctx, dbgen.InsertRoomInvitationCredentialParams{ConversationID: string(c.ID), Password: password}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RoomInvitationCredential(ctx context.Context, id domain.ID) (string, error) {
	password, err := s.queries.GetRoomInvitationCredential(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return password, err
}
