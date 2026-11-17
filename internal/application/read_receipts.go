package application

import (
	"context"
	"strings"

	"github.com/araihu/oie/internal/domain"
)

type DisplayedMarkerProtocol interface {
	SendDisplayedMarker(context.Context, domain.Conversation, domain.DisplayedMarker)
}

type DisplayedMarkerHost interface {
	IsCurrentDisplayedMarker(context.Context, domain.DisplayedMarker) (bool, error)
}

type displayedMarkerRepository interface {
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

func (e *Engine) IsCurrentDisplayedMarker(ctx context.Context, marker domain.DisplayedMarker) (bool, error) {
	repo, ok := e.repo.(displayedMarkerRepository)
	if !ok {
		return false, ErrProtocolUnavailable
	}
	return repo.IsCurrentDisplayedHead(ctx, marker)
}

func (e *Engine) runDisplayedMarkers(ctx context.Context, protocol DisplayedMarkerProtocol) error {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case marker := <-e.displayedWork:
			c, err := e.Conversation(ctx, marker.ConversationID)
			if err != nil || c.AccountID != marker.AccountID || c.Request != "" || c.RemovedFromChats {
				continue
			}
			protocol.SendDisplayedMarker(ctx, c, marker)
		}
	}
	return ctx.Err()
}

type readReceiptPolicyRepository interface {
	GlobalReadReceipts(context.Context) (domain.GlobalReadReceipts, error)
	SetGlobalReadReceipts(context.Context, domain.GlobalReadReceipts) (domain.GlobalReadReceipts, error)
	ConversationReadReceipts(context.Context, domain.ID) (domain.ConversationReadReceipts, error)
	SetConversationReadReceipts(context.Context, domain.ID, string, *bool) (domain.ConversationReadReceipts, error)
}

func (e *Engine) GlobalReadReceipts(ctx context.Context) (domain.GlobalReadReceipts, error) {
	repo, ok := e.repo.(readReceiptPolicyRepository)
	if !ok {
		return domain.GlobalReadReceipts{}, ErrProtocolUnavailable
	}
	return repo.GlobalReadReceipts(ctx)
}

func (e *Engine) SetGlobalReadReceipts(ctx context.Context, policy domain.GlobalReadReceipts) (domain.GlobalReadReceipts, error) {
	repo, ok := e.repo.(readReceiptPolicyRepository)
	if !ok {
		return domain.GlobalReadReceipts{}, ErrProtocolUnavailable
	}
	if !validRetentionRevision(policy.Revision) {
		return domain.GlobalReadReceipts{}, ErrInvalid
	}
	return repo.SetGlobalReadReceipts(ctx, policy)
}

func (e *Engine) ConversationReadReceipts(ctx context.Context, id domain.ID) (domain.ConversationReadReceipts, error) {
	repo, ok := e.repo.(readReceiptPolicyRepository)
	if !ok {
		return domain.ConversationReadReceipts{}, ErrProtocolUnavailable
	}
	if !validID(id) {
		return domain.ConversationReadReceipts{}, ErrInvalid
	}
	return repo.ConversationReadReceipts(ctx, id)
}

func (e *Engine) SetConversationReadReceipts(ctx context.Context, id domain.ID, revision string, override *bool) (domain.ConversationReadReceipts, error) {
	repo, ok := e.repo.(readReceiptPolicyRepository)
	if !ok {
		return domain.ConversationReadReceipts{}, ErrProtocolUnavailable
	}
	global, local, _ := strings.Cut(revision, ":")
	if !validID(id) || !validRetentionRevision(global) || !validRetentionRevision(local) {
		return domain.ConversationReadReceipts{}, ErrInvalid
	}
	return repo.SetConversationReadReceipts(ctx, id, revision, override)
}
