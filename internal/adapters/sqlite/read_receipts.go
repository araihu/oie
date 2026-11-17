package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func prepareDisplayedMarker(ctx context.Context, q *dbgen.Queries, c domain.Conversation, revision domain.ID, newlyRead int, transport string) (*domain.DisplayedMarker, error) {
	if newlyRead == 0 || transport == "" || c.Request != "" || c.RemovedFromChats {
		return nil, nil
	}
	policy, err := readConversationReadReceipts(ctx, q, c.ID)
	if err != nil || !policy.EffectiveEnabled {
		return nil, err
	}
	observationEpoch := policy.PolicyEpoch + ":" + transport
	// Record connected reads while the effective policy allows receipts.
	// A future read may close a gap before this already read candidate.
	if err := q.RecordDisplayedPolicyEpoch(ctx, dbgen.RecordDisplayedPolicyEpochParams{
		ReadRevisionID: string(revision), Epoch: &observationEpoch,
	}); err != nil {
		return nil, err
	}
	target, err := latestDisplayedTarget(ctx, q, c.ID, observationEpoch, c.UnreadCount != 0)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &domain.DisplayedMarker{
		ConversationID: c.ID, AccountID: c.AccountID, MessageID: domain.ID(target.ID),
		PolicyEpoch: policy.PolicyEpoch, RemoteID: target.RemoteID, Transport: transport,
	}, nil
}

func latestDisplayedTarget(ctx context.Context, q *dbgen.Queries, id domain.ID, epoch string, hasUnread bool) (dbgen.LatestDisplayedTargetRow, error) {
	var firstUnread string
	if hasUnread {
		var err error
		firstUnread, err = q.FirstUnreadMessage(ctx, string(id))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return dbgen.LatestDisplayedTargetRow{}, err
		}
	}
	return q.LatestDisplayedTarget(ctx, dbgen.LatestDisplayedTargetParams{
		ConversationID: string(id), FirstUnreadID: firstUnread, PolicyEpoch: &epoch,
	})
}

func (s *Store) ClaimDisplayedMarker(ctx context.Context, marker domain.DisplayedMarker) (bool, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.AcquireWriteLock(ctx); err != nil {
		return false, err
	}
	policy, err := readConversationReadReceipts(ctx, q, marker.ConversationID)
	if errors.Is(err, application.ErrNotFound) {
		return false, nil
	}
	if err != nil || !policy.EffectiveEnabled || policy.PolicyEpoch != marker.PolicyEpoch {
		return false, err
	}
	// Recheck the frontier before attempting a wire write. A delayed message
	// can introduce an unread gap after the browser's read transaction.
	chat, err := q.GetConversation(ctx, string(marker.ConversationID))
	if err != nil || chat.AccountID != string(marker.AccountID) {
		return false, err
	}
	target, err := latestDisplayedTarget(ctx, q, marker.ConversationID, marker.PolicyEpoch+":"+marker.Transport, chat.UnreadCount != 0)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || target.ID != string(marker.MessageID) || target.RemoteID != marker.RemoteID {
		return false, err
	}
	count, err := q.AdvanceDisplayedHead(ctx, dbgen.AdvanceDisplayedHeadParams{
		ConversationID: string(marker.ConversationID), PositionMs: target.PositionMs, MessageID: target.ID,
	})
	if err != nil || count == 0 {
		return false, err
	}
	return true, tx.Commit()
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
		Revision: row.Revision, PolicyEpoch: row.PolicyEpoch, GlobalEnabled: row.GlobalEnabled != 0, EffectiveEnabled: row.EffectiveEnabled != 0,
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
