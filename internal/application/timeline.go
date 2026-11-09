package application

import (
	"context"
	"github.com/araihu/oie/internal/domain"
)

// MessageTimelineRepository optionally provides nearest newer retained messages.
// Results are ordered ascending by original creation time and UUID. The caller
// requests one lookahead row; reads never initiate protocol history retrieval.
type MessageTimelineRepository interface {
	MessagesAfter(context.Context, domain.ID, domain.ID, int) ([]domain.Message, error)
}

// NewerMessages reads the nearest messages following a retained scoped anchor.
// It returns ascending rows plus one possible lookahead row for HTTP pagination.
func (e *Engine) NewerMessages(ctx context.Context, conversation, cursor domain.ID, limit int) ([]domain.Message, error) {
	if !validID(cursor) || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	c, err := e.Conversation(ctx, conversation)
	if err != nil {
		return nil, err
	}
	anchor, err := e.Message(ctx, cursor)
	if err != nil {
		return nil, err
	}
	if anchor.ConversationID != c.ID || anchor.AccountID != c.AccountID {
		return nil, ErrNotFound
	}
	repo, ok := e.repo.(MessageTimelineRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return repo.MessagesAfter(ctx, c.ID, anchor.ID, limit+1)
}
