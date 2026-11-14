package application

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type RetentionPeer struct {
	Address string
	Days    int
}

type RetentionHost interface {
	RetentionDays(context.Context, domain.ID, string, string) (int, error)
	DirectRetentionPeers(context.Context, domain.ID) ([]RetentionPeer, error)
}

type retentionRepository interface {
	RetentionHost
	GlobalRetention(context.Context) (domain.GlobalRetention, error)
	ConversationRetention(context.Context, domain.ID) (domain.ConversationRetention, error)
	SetGlobalRetention(context.Context, domain.GlobalRetention) (domain.GlobalRetention, []domain.ID, error)
	SetConversationRetention(context.Context, domain.ID, string, *int) (domain.ConversationRetention, []domain.ID, error)
	PurgeExpiredHistory(context.Context, time.Time, NewID) (int, error)
}

func (e *Engine) GlobalRetention(ctx context.Context) (domain.GlobalRetention, error) {
	repo, ok := e.repo.(retentionRepository)
	if !ok {
		return domain.GlobalRetention{}, ErrProtocolUnavailable
	}
	return repo.GlobalRetention(ctx)
}

func (e *Engine) ConversationRetention(ctx context.Context, id domain.ID) (domain.ConversationRetention, error) {
	if !validID(id) {
		return domain.ConversationRetention{}, ErrInvalid
	}
	repo, ok := e.repo.(retentionRepository)
	if !ok {
		return domain.ConversationRetention{}, ErrProtocolUnavailable
	}
	return repo.ConversationRetention(ctx, id)
}

func validRetentionRevision(revision string) bool {
	n, err := strconv.ParseInt(revision, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == revision
}

func validRetentionDays(days int) bool { return days >= 0 && days <= domain.MaxRetentionDays }

func (e *Engine) SetGlobalRetention(ctx context.Context, in domain.GlobalRetention) (domain.GlobalRetention, error) {
	if !validRetentionRevision(in.Revision) || !validRetentionDays(in.RoomDays) || !validRetentionDays(in.DirectDays) {
		return domain.GlobalRetention{}, ErrInvalid
	}
	repo, ok := e.repo.(retentionRepository)
	if !ok {
		return domain.GlobalRetention{}, ErrProtocolUnavailable
	}
	value, accounts, err := repo.SetGlobalRetention(ctx, in)
	if err == nil {
		e.retentionChanged(accounts)
	}
	return value, err
}

func (e *Engine) SetConversationRetention(ctx context.Context, id domain.ID, expected string, days *int) (domain.ConversationRetention, error) {
	if !validID(id) || !validRetentionRevision(expected) || days != nil && !validRetentionDays(*days) {
		return domain.ConversationRetention{}, ErrInvalid
	}
	repo, ok := e.repo.(retentionRepository)
	if !ok {
		return domain.ConversationRetention{}, ErrProtocolUnavailable
	}
	value, accounts, err := repo.SetConversationRetention(ctx, id, expected, days)
	if err == nil {
		e.retentionChanged(accounts)
	}
	return value, err
}

func (e *Engine) RetentionDays(ctx context.Context, account domain.ID, address, kind string) (int, error) {
	if repo, ok := e.repo.(retentionRepository); ok {
		return repo.RetentionDays(ctx, account, address, kind)
	}
	return 0, nil
}

func (e *Engine) DirectRetentionPeers(ctx context.Context, account domain.ID) ([]RetentionPeer, error) {
	if repo, ok := e.repo.(retentionRepository); ok {
		return repo.DirectRetentionPeers(ctx, account)
	}
	return nil, nil
}

func (e *Engine) retentionChanged(accounts []domain.ID) {
	if protocol, ok := e.protocols.(RoomArchiveProtocol); ok {
		for _, account := range accounts {
			protocol.WakeRoomArchive(account)
		}
	}
	select {
	case e.retentionWork <- struct{}{}:
	default:
	}
}

func (e *Engine) runRetention(ctx context.Context, repo retentionRepository) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for ctx.Err() == nil {
		count, err := repo.PurgeExpiredHistory(ctx, e.now().UTC(), e.newID)
		if count != 0 {
			e.notifyEvents()
		}
		if err != nil && ctx.Err() == nil {
			slog.Error("expire message history", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-e.retentionWork:
		}
	}
	return ctx.Err()
}
