package application

import (
	"context"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type ConversationRequestRepository interface {
	PendingDirectHistoryChecks(context.Context, domain.ID) ([]domain.Conversation, error)
	ResolveDirectHistoryCheck(context.Context, domain.ID, domain.DirectHistoryCheck, bool, domain.ID, time.Time) error
	StoreRoomInvitation(context.Context, domain.Conversation, string) error
	RoomInvitationCredential(context.Context, domain.ID) (string, error)
}

// ConversationRequestHost is used by the authenticated protocol session.
type ConversationRequestHost interface {
	PendingDirectHistoryChecks(context.Context, domain.ID) ([]domain.Conversation, error)
	ResolveDirectHistoryCheck(context.Context, domain.ID, domain.DirectHistoryCheck, bool) error
	ReceiveRoomInvitation(context.Context, domain.ID, string, string) error
}

func (e *Engine) PendingDirectHistoryChecks(ctx context.Context, account domain.ID) ([]domain.Conversation, error) {
	r, ok := e.repo.(ConversationRequestRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r.PendingDirectHistoryChecks(ctx, account)
}

func (e *Engine) ResolveDirectHistoryCheck(ctx context.Context, id domain.ID, check domain.DirectHistoryCheck, prior bool) error {
	r, ok := e.repo.(ConversationRequestRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	rev, err := e.newID()
	if err != nil {
		return err
	}
	if err = r.ResolveDirectHistoryCheck(ctx, id, check, prior, rev, e.now().UTC()); err == nil {
		e.notifyEvents()
	}
	return err
}

func (e *Engine) ReceiveRoomInvitation(ctx context.Context, accountID domain.ID, address, password string) error {
	r, ok := e.repo.(ConversationRequestRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	if len(password) > 1024 || strings.ContainsRune(password, 0) {
		return ErrInvalid
	}
	a, err := e.Account(ctx, accountID)
	if err != nil {
		return err
	}
	address, err = e.canonical(a.Protocol, address)
	if err != nil {
		return err
	}
	id, err := e.newID()
	if err != nil {
		return err
	}
	rev, err := e.newID()
	if err != nil {
		return err
	}
	peer, err := e.newID()
	if err != nil {
		return err
	}
	c := domain.Conversation{ID: id, RevisionID: rev, PeerID: peer, AccountID: accountID, Kind: "room", Address: address, Title: address, UpdatedAt: e.now().UTC(), Request: domain.RoomInvitation}
	if err = r.StoreRoomInvitation(ctx, c, password); err == nil {
		e.notifyEvents()
	}
	return err
}
