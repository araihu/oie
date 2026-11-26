package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

// DetailProvider returns a cached observation and may enqueue bounded background
// discovery. It must never join rooms, probe peers or change subscriptions.
type DetailProvider interface {
	ConversationInfo(context.Context, domain.Account, domain.Conversation) (domain.ConversationInfo, error)
}

// RoomTitleHost receives names from discovery for rooms already saved by this account.
type RoomTitleHost interface {
	NeedsRoomTitleFallback(context.Context, domain.ID, string) bool
	ObserveRoomTitle(context.Context, domain.ID, string, string) error
	ObserveRoomTitleFallback(context.Context, domain.ID, string, string) error
}

type roomTitleRepository interface {
	RoomBookmarkRepository
	UpdateRoomTitle(context.Context, domain.ID, string, bool, domain.ID, time.Time) error
}

func (e *Engine) NeedsRoomTitleFallback(ctx context.Context, account domain.ID, address string) bool {
	repo, ok := e.repo.(RoomBookmarkRepository)
	if !ok {
		return false
	}
	room, err := repo.RestoredRoom(ctx, account, address)
	return err == nil && room.NeedsRoomTitle()
}

func (e *Engine) ObserveRoomTitle(ctx context.Context, account domain.ID, address, title string) error {
	return e.observeRoomTitle(ctx, account, address, title, false)
}

func (e *Engine) ObserveRoomTitleFallback(ctx context.Context, account domain.ID, address, title string) error {
	return e.observeRoomTitle(ctx, account, address, title, true)
}

func (e *Engine) observeRoomTitle(ctx context.Context, account domain.ID, address, title string, fallback bool) error {
	title = strings.TrimSpace(title)
	if title == "" || title == address {
		return nil
	}
	repo, ok := e.repo.(roomTitleRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	room, err := repo.RestoredRoom(ctx, account, address)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil || !room.AcceptsRoomTitle(title, fallback) {
		return err
	}
	revision, err := e.newID()
	if err != nil {
		return err
	}
	if err := repo.UpdateRoomTitle(ctx, room.ID, title, fallback, revision, e.now().UTC()); err != nil {
		return err
	}
	e.notifyEvents()
	return nil
}

func (e *Engine) ConversationDetails(ctx context.Context, id domain.ID) (domain.ConversationDetails, error) {
	c, err := e.Conversation(ctx, id)
	if err != nil {
		return domain.ConversationDetails{}, err
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return domain.ConversationDetails{}, err
	}
	status, err := e.Connection(ctx, c.AccountID)
	if err != nil {
		return domain.ConversationDetails{}, err
	}
	result := domain.ConversationDetails{ConversationID: c.ID, AccountID: a.ID, Kind: c.Kind, Address: c.Address, AccountConnection: status, ConversationInfo: domain.EmptyConversationInfo(c.Kind, "not_connected")}
	if status.State != "connected" {
		return result, nil
	}
	result.ConversationInfo = domain.EmptyConversationInfo(c.Kind, "unsupported")
	if provider, ok := e.protocols.(DetailProvider); ok {
		info, err := provider.ConversationInfo(ctx, a, c)
		if err != nil {
			return result, err
		}
		result.ConversationInfo = info
		if info.State == "not_connected" {
			current, err := e.Connection(ctx, a.ID)
			if err != nil {
				return result, err
			}
			result.AccountConnection = current
		}
	}
	return result, nil
}
