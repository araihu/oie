package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

type IncomingRoomModeration struct {
	StanzaID string
	domain.MessageModeration
}

type roomModerationRepository interface {
	RecordRoomModeration(context.Context, domain.ID, string, IncomingRoomModeration) error
}

func (e *Engine) receiveRoomModeration(ctx context.Context, in Incoming) error {
	repo, ok := e.repo.(roomModerationRepository)
	if !ok {
		return ErrProtocolUnavailable
	}
	data := *in.Moderation
	if in.Kind != "room" || data.At.IsZero() || !utf8.ValidString(data.StanzaID) || data.StanzaID == "" ||
		len(data.StanzaID) > 4096 || strings.ContainsRune(data.StanzaID, 0) || !roomManagementText(data.Reason, 500) {
		return ErrInvalid
	}
	if _, _, err := e.roomTarget(ctx, in.AccountID, in.Address); err != nil {
		return err
	}
	if err := repo.RecordRoomModeration(ctx, in.AccountID, in.Address, data); err != nil {
		return err
	}
	e.notifyEvents()
	if !in.ModerationTombstone {
		return nil
	}
	return e.receiveText(ctx, in)
}
