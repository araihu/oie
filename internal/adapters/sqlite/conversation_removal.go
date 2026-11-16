package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Store) RemoveConversationFromChats(ctx context.Context, in application.ConversationRemoval) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, true)
	if err != nil || receipt != nil {
		return err
	}
	c, err := readConversation(ctx, q, in.ResourceID)
	if err != nil {
		return err
	}
	if c.AccountID != in.AccountID || c.Kind != in.Kind {
		return application.ErrNotFound
	}
	if c.RevisionID != in.ExpectedRevisionID || c.RemovedFromChats {
		return application.ErrStateConflict
	}
	next := c
	next.RemovedFromChats = true
	if c.Kind == "room" {
		if err := stopRemovedRoom(ctx, q, c, in); err != nil {
			return err
		}
	} else {
		next.Archived = false
	}
	if err := writeConversationDecision(ctx, q, c, next, in.RevisionID, in.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func stopRemovedRoom(ctx context.Context, q *dbgen.Queries, c domain.Conversation, in application.ConversationRemoval) error {
	row, err := q.GetRoomIntent(ctx, string(c.ID))
	if !errors.Is(err, sql.ErrNoRows) && err != nil {
		return err
	}
	intent := domain.RoomSession{AccountID: c.AccountID, ConversationID: c.ID, IntentID: in.RoomIntentRevisionID,
		IntentRevisionID: in.RoomIntentRevisionID, State: "not_joined", UpdatedAt: in.CreatedAt}
	if err == nil {
		intent.IntentID, intent.Nickname = domain.ID(row.ID), row.Nickname
	}
	if err := putRoomIntent(ctx, q, intent); err != nil {
		return err
	}
	if err := q.ClearRoomReactionVisibility(ctx, dbgen.ClearRoomReactionVisibilityParams{AccountID: string(c.AccountID), Address: c.Address}); err != nil {
		return err
	}
	_, err = change(ctx, q, "conversation.room_session_changed", intent, in.CreatedAt)
	return err
}
