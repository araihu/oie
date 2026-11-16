package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

const (
	RemoveRoomOperation   = "remove_room_from_chats"
	RemoveDirectOperation = "remove_direct_from_chats"
)

type ConversationRemovalRepository interface {
	RemoveConversationFromChats(context.Context, ConversationRemoval) error
}

type ConversationRemoval struct {
	Operation
	Fingerprint, Kind                string
	AccountID, ExpectedRevisionID    domain.ID
	RevisionID, RoomIntentRevisionID domain.ID
}

func (e *Engine) RemoveRoomFromChats(ctx context.Context, key, id, account, expected domain.ID) error {
	return e.removeFromChats(ctx, key, id, account, expected, "room", RemoveRoomOperation)
}

func (e *Engine) RemoveDirectFromChats(ctx context.Context, key, id, account, expected domain.ID) error {
	return e.removeFromChats(ctx, key, id, account, expected, "direct", RemoveDirectOperation)
}

func (e *Engine) removeFromChats(ctx context.Context, key, id, account, expected domain.ID, kind, operationKind string) error {
	if !validID(key) || !validID(id) || !validID(account) || !validID(expected) {
		return ErrInvalid
	}
	repo, ok := e.repo.(ConversationRemovalRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	revision, err := e.newID()
	if err != nil {
		return err
	}
	var intentRevision domain.ID
	if kind == "room" {
		intentRevision, err = e.newID()
		if err != nil {
			return err
		}
	}
	err = repo.RemoveConversationFromChats(ctx, ConversationRemoval{
		Operation:   Operation{ID: key, ResourceID: id, Kind: operationKind, CreatedAt: e.now().UTC()},
		Fingerprint: fingerprint(operationKind, []domain.ID{id, account, expected}),
		Kind:        kind, AccountID: account, ExpectedRevisionID: expected, RevisionID: revision, RoomIntentRevisionID: intentRevision,
	})
	if err == nil {
		if kind == "room" {
			e.wakeRooms()
		}
		e.outgoingWorkCommitted()
	}
	return err
}
