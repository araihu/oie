package application

import (
	"context"
	"strings"

	"github.com/araihu/oie/internal/domain"
)

type DisplayedMarkerProtocol interface {
	DisplayedMarkerTransport(domain.ID) string
	SendDisplayedMarker(context.Context, domain.Conversation, domain.DisplayedMarker)
}

type DisplayedMarkerHost interface {
	ClaimDisplayedMarker(context.Context, domain.DisplayedMarker) (bool, error)
}

type displayedMarkerRepository interface {
	ClaimDisplayedMarker(context.Context, domain.DisplayedMarker) (bool, error)
}

func (e *Engine) displayedMarkerTransport(account domain.ID) string {
	protocol, ok := e.protocols.(DisplayedMarkerProtocol)
	if !ok {
		return ""
	}
	return protocol.DisplayedMarkerTransport(account)
}

func (e *Engine) queueDisplayedMarker(marker *domain.DisplayedMarker) {
	if marker == nil {
		return
	}
	e.displayedMu.Lock()
	e.displayedPending = append(e.displayedPending, *marker)
	e.displayedMu.Unlock()
	select {
	case e.displayedWork <- struct{}{}:
	default:
	}
}

func (e *Engine) ClaimDisplayedMarker(ctx context.Context, marker domain.DisplayedMarker) (bool, error) {
	repo, ok := e.repo.(displayedMarkerRepository)
	if !ok {
		return false, ErrProtocolUnavailable
	}
	return repo.ClaimDisplayedMarker(ctx, marker)
}

func (e *Engine) runDisplayedMarkers(ctx context.Context, protocol DisplayedMarkerProtocol) error {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.displayedWork:
			e.displayedMu.Lock()
			pending := e.displayedPending
			e.displayedPending = nil
			e.displayedMu.Unlock()
			for _, marker := range pending {
				if err := ctx.Err(); err != nil {
					return err
				}
				c, err := e.Conversation(ctx, marker.ConversationID)
				if err != nil || c.AccountID != marker.AccountID || c.Request != "" || c.RemovedFromChats {
					continue
				}
				protocol.SendDisplayedMarker(ctx, c, marker)
			}
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
