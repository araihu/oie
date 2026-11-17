package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func reserveDisplayedMarker(ctx context.Context, q *dbgen.Queries, c domain.Conversation, revision domain.ID, newlyRead int) (*domain.DisplayedMarker, error) {
	if newlyRead == 0 || c.Request != "" || c.RemovedFromChats {
		return nil, nil
	}
	prefs, err := accountCommunicationPreferences(ctx, q, c.AccountID)
	if err != nil || !prefs.SendReadReceipts {
		return nil, err
	}
	firstUnread, err := q.FirstUnreadMessage(ctx, string(c.ID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	target, err := q.LatestDisplayedTarget(ctx, dbgen.LatestDisplayedTargetParams{ConversationID: string(c.ID), FirstUnreadID: firstUnread, ReadRevisionID: string(revision)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	count, err := q.AdvanceDisplayedHead(ctx, dbgen.AdvanceDisplayedHeadParams{ConversationID: string(c.ID), PositionMs: target.PositionMs, MessageID: target.ID})
	if err != nil || count == 0 {
		return nil, err
	}
	return &domain.DisplayedMarker{
		ConversationID: c.ID, AccountID: c.AccountID, MessageID: domain.ID(target.ID),
		PreferencesRevision: prefs.RevisionID, RemoteID: target.RemoteID,
	}, nil
}

func (s *Store) IsCurrentDisplayedHead(ctx context.Context, marker domain.DisplayedMarker) (bool, error) {
	return s.read.IsCurrentDisplayedHead(ctx, dbgen.IsCurrentDisplayedHeadParams{
		ConversationID: string(marker.ConversationID), MessageID: string(marker.MessageID),
	})
}
