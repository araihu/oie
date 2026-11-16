package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

const RemoveRoomOperation = "remove_room_from_chats"

type RoomRemovalRepository interface {
	RemoveRoomFromChats(context.Context, RoomRemoval) error
}

type RoomRemoval struct {
	Operation
	Fingerprint                      string
	AccountID, ExpectedRevisionID    domain.ID
	RevisionID, RoomIntentRevisionID domain.ID
}

func (e *Engine) RemoveRoomFromChats(ctx context.Context, key, id, account, expected domain.ID) error {
	if !validID(key) || !validID(id) || !validID(account) || !validID(expected) {
		return ErrInvalid
	}
	repo, ok := e.repo.(RoomRemovalRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	revision, err := e.newID()
	if err != nil {
		return err
	}
	intentRevision, err := e.newID()
	if err != nil {
		return err
	}
	err = repo.RemoveRoomFromChats(ctx, RoomRemoval{
		Operation:   Operation{ID: key, ResourceID: id, Kind: RemoveRoomOperation, CreatedAt: e.now().UTC()},
		Fingerprint: fingerprint(RemoveRoomOperation, []domain.ID{id, account, expected}),
		AccountID:   account, ExpectedRevisionID: expected, RevisionID: revision, RoomIntentRevisionID: intentRevision,
	})
	if err == nil {
		e.wakeRooms()
		e.outgoingWorkCommitted()
	}
	return err
}
