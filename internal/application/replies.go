package application

import (
	"context"
	"github.com/araihu/oie/internal/domain"
)

// OutgoingReferenceProvider describes IDs written by a protocol adapter without
// coupling the core to its wire format. Sending replies is a separate operation.
type OutgoingReferenceProvider interface {
	OutgoingReferences(domain.Account, Outgoing) []domain.MessageReference
}
type ContextRepository interface {
	MessageContext(context.Context, domain.ID, domain.ID, int) (domain.MessageContext, error)
}

func (e *Engine) MessageContext(ctx context.Context, conversation, target domain.ID, limit int) (domain.MessageContext, error) {
	if !validID(conversation) || !validID(target) || limit < 1 || limit > 100 {
		return domain.MessageContext{}, ErrInvalid
	}
	if _, err := e.Conversation(ctx, conversation); err != nil {
		return domain.MessageContext{}, err
	}
	r, ok := e.repo.(ContextRepository)
	if !ok {
		return domain.MessageContext{}, ErrProtocolUnavailable
	}
	return r.MessageContext(ctx, conversation, target, limit)
}
