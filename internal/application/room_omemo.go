package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

type RoomOMEMOState struct {
	Address           string
	Enabled, Eligible bool
	Members           []string
}

type RoomOMEMOProtocol interface {
	RoomEncryption(domain.Account, string) (bool, error)
	InspectRoomOMEMO(context.Context, domain.Account, string) (RoomOMEMOState, error)
	SetRoomOMEMO(context.Context, domain.Account, string, bool) error
}

func (e *Engine) roomOMEMOScope(ctx context.Context, id domain.ID) (RoomOMEMOProtocol, domain.Account, domain.Conversation, error) {
	var account domain.Account
	p, ok := e.protocols.(RoomOMEMOProtocol)
	if !ok || !e.OMEMOEnabled() {
		return nil, account, domain.Conversation{}, ErrProtocolUnavailable
	}
	room, err := e.Conversation(ctx, id)
	if err != nil {
		return nil, account, room, err
	}
	if room.Kind != "room" || room.Request != "" || room.RemovedFromChats {
		return nil, account, room, ErrNotFound
	}
	account, err = e.Account(ctx, room.AccountID)
	return p, account, room, err
}

func (e *Engine) InspectRoomOMEMO(ctx context.Context, id domain.ID) (RoomOMEMOState, error) {
	p, a, room, err := e.roomOMEMOScope(ctx, id)
	if err != nil {
		return RoomOMEMOState{}, err
	}
	return p.InspectRoomOMEMO(ctx, a, room.Address)
}

func (e *Engine) SetRoomOMEMO(ctx context.Context, id domain.ID, enabled bool) error {
	p, a, room, err := e.roomOMEMOScope(ctx, id)
	if err != nil {
		return err
	}
	return p.SetRoomOMEMO(ctx, a, room.Address, enabled)
}
