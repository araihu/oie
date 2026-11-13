package application

import (
	"context"
	"strings"

	"github.com/araihu/oie/internal/domain"
)

type RoomDiscoveryProvider interface {
	DiscoverRooms(context.Context, domain.Account, string) (domain.RoomDirectory, error)
}

func (e *Engine) DiscoverRooms(ctx context.Context, id domain.ID, service string) (domain.RoomDirectory, error) {
	a, err := e.Account(ctx, id)
	if err != nil {
		return domain.RoomDirectory{}, err
	}
	service = strings.TrimSpace(service)
	if a.Protocol != "xmpp" || strings.ContainsAny(service, "@/?:#") || !roomManagementText(service, 253) {
		return domain.RoomDirectory{}, ErrInvalid
	}
	p, ok := e.protocols.(RoomDiscoveryProvider)
	if !ok {
		return domain.RoomDirectory{}, ErrProtocolUnavailable
	}
	return p.DiscoverRooms(ctx, a, service)
}
