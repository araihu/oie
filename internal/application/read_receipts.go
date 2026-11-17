package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

type DisplayedMarkerProtocol interface {
	SendDisplayedMarker(context.Context, domain.Conversation, domain.DisplayedMarker)
}

type DisplayedMarkerRepository interface {
	IsCurrentDisplayedHead(context.Context, domain.DisplayedMarker) (bool, error)
}

func (e *Engine) queueDisplayedMarker(marker *domain.DisplayedMarker) {
	if _, ok := e.protocols.(DisplayedMarkerProtocol); !ok || marker == nil {
		return
	}
	// Markers are best-effort and have no replay after restart or disconnect.
	select {
	case e.displayedWork <- *marker:
	default:
	}
}

func (e *Engine) runDisplayedMarkers(ctx context.Context, protocol DisplayedMarkerProtocol, repo DisplayedMarkerRepository) error {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case marker := <-e.displayedWork:
			c, err := e.Conversation(ctx, marker.ConversationID)
			if err != nil || c.AccountID != marker.AccountID || c.Request != "" || c.RemovedFromChats {
				continue
			}
			if current, err := repo.IsCurrentDisplayedHead(ctx, marker); err == nil && current {
				protocol.SendDisplayedMarker(ctx, c, marker)
			}
		}
	}
	return ctx.Err()
}
