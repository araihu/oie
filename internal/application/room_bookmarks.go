package application

import (
	"context"
	"errors"

	"github.com/araihu/oie/internal/domain"
)

// RoomBookmark is provider configuration, not evidence of a joined session.
// The adapter supplies its account-default nickname; protocol syntax stays out
// of the application. Existing local intents always take precedence.
type RoomBookmark struct {
	AccountID      domain.ID
	Address, Title string
	AutoJoin       bool
	Nickname       string
}

type RoomBookmarkHost interface {
	RestoreRoomBookmark(context.Context, RoomBookmark) error
}

type RoomBookmarkRepository interface {
	RestoredRoom(context.Context, domain.ID, string) (domain.Conversation, error)
}

// RestoreRoomBookmark imports a saved room, then seeds a join intent only when
// the server requests autojoin and no local join, leave or revocation exists.
func (e *Engine) RestoreRoomBookmark(ctx context.Context, bookmark RoomBookmark) error {
	if err := e.RestoreConversation(ctx, bookmark.AccountID, bookmark.Address, "room", bookmark.Title); err != nil {
		return err
	}
	if !bookmark.AutoJoin {
		return nil
	}
	a, err := e.Account(ctx, bookmark.AccountID)
	if err != nil {
		return err
	}
	address, err := e.canonical(a.Protocol, bookmark.Address)
	if err != nil {
		return err
	}
	r, ok := e.repo.(RoomBookmarkRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	c, err := r.RestoredRoom(ctx, a.ID, address)
	if err != nil {
		return err
	}
	if c.Kind != "room" || c.AccountID != a.ID || c.Address != address {
		return ErrInvalid
	}
	if c.Request == domain.RoomInvitation {
		return nil
	}
	intents, ok := e.repo.(RoomIntentRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	if _, err = intents.RoomIntent(ctx, c.ID); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	key, err := e.newID()
	if err != nil {
		return err
	}
	_, err = e.JoinRoom(ctx, key, c.ID, bookmark.Nickname, "")
	// An explicit command may win between the read and the first-intent CAS.
	// Never retry that race against the newer local decision.
	var conflict RoomStateConflict
	if errors.As(err, &conflict) {
		return nil
	}
	return err
}
