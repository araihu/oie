package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func reserveDisplayedMarker(ctx context.Context, q *dbgen.Queries, c domain.Conversation, revision domain.ID, newlyRead int) (*domain.DisplayedMarker, error) {
	if newlyRead == 0 || c.Request != "" || c.RemovedFromChats {
		return nil, nil
	}
	policy, err := readConversationReadReceipts(ctx, q, c.ID)
	if err != nil || !policy.EffectiveEnabled {
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
		PolicyRevision: policy.Revision, RemoteID: target.RemoteID,
	}, nil
}

func (s *Store) IsCurrentDisplayedHead(ctx context.Context, marker domain.DisplayedMarker) (bool, error) {
	return s.read.IsCurrentDisplayedHead(ctx, dbgen.IsCurrentDisplayedHeadParams{
		ConversationID: string(marker.ConversationID), MessageID: string(marker.MessageID), PolicyRevision: marker.PolicyRevision,
	})
}

func readGlobalReadReceipts(ctx context.Context, q *dbgen.Queries) (domain.GlobalReadReceipts, error) {
	row, err := q.GetGlobalReadReceipts(ctx)
	return domain.GlobalReadReceipts{Revision: row.Revision, Enabled: row.Enabled != 0}, err
}

func (s *Store) GlobalReadReceipts(ctx context.Context) (domain.GlobalReadReceipts, error) {
	return readGlobalReadReceipts(ctx, s.read)
}

func (s *Store) SetGlobalReadReceipts(ctx context.Context, desired domain.GlobalReadReceipts) (domain.GlobalReadReceipts, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.GlobalReadReceipts{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.AcquireWriteLock(ctx); err != nil {
		return domain.GlobalReadReceipts{}, err
	}
	current, err := readGlobalReadReceipts(ctx, q)
	if err != nil {
		return current, err
	}
	if current.Revision != desired.Revision {
		return current, application.ErrStateConflict
	}
	if current.Enabled == desired.Enabled {
		return current, nil
	}
	if err := q.PutGlobalReadReceipts(ctx, number(desired.Enabled)); err != nil {
		return current, err
	}
	updated, err := readGlobalReadReceipts(ctx, q)
	if err != nil {
		return current, err
	}
	return updated, tx.Commit()
}

func readConversationReadReceipts(ctx context.Context, q *dbgen.Queries, id domain.ID) (domain.ConversationReadReceipts, error) {
	row, err := q.GetConversationReadReceipts(ctx, string(id))
	if err != nil {
		return domain.ConversationReadReceipts{}, notFound(err)
	}
	policy := domain.ConversationReadReceipts{
		Revision: row.Revision, GlobalEnabled: row.GlobalEnabled != 0, EffectiveEnabled: row.EffectiveEnabled != 0,
	}
	if row.OverrideEnabled != nil {
		policy.Override = new(*row.OverrideEnabled != 0)
	}
	return policy, nil
}

func (s *Store) ConversationReadReceipts(ctx context.Context, id domain.ID) (domain.ConversationReadReceipts, error) {
	return readConversationReadReceipts(ctx, s.read, id)
}

func (s *Store) SetConversationReadReceipts(ctx context.Context, id domain.ID, revision string, override *bool) (domain.ConversationReadReceipts, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.ConversationReadReceipts{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.AcquireWriteLock(ctx); err != nil {
		return domain.ConversationReadReceipts{}, err
	}
	current, err := readConversationReadReceipts(ctx, q, id)
	if err != nil {
		return current, err
	}
	if current.Revision != revision {
		return current, application.ErrStateConflict
	}
	if current.Override == nil && override == nil || current.Override != nil && override != nil && *current.Override == *override {
		return current, nil
	}
	var enabled *int64
	if override != nil {
		enabled = new(number(*override))
	}
	if err := q.PutConversationReadReceipts(ctx, dbgen.PutConversationReadReceiptsParams{ConversationID: string(id), Enabled: enabled}); err != nil {
		return current, err
	}
	updated, err := readConversationReadReceipts(ctx, q, id)
	if err != nil {
		return current, err
	}
	return updated, tx.Commit()
}
