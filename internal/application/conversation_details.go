package application

import (
	"context"
	"github.com/araihu/oie/internal/domain"
)

// DetailProvider returns a cached observation and may enqueue bounded background
// discovery. It must never join rooms, probe peers or change subscriptions.
type DetailProvider interface {
	ConversationInfo(context.Context, domain.Account, domain.Conversation) (domain.ConversationInfo, error)
}

func (e *Engine) ConversationDetails(ctx context.Context, id domain.ID) (domain.ConversationDetails, error) {
	c, err := e.Conversation(ctx, id)
	if err != nil {
		return domain.ConversationDetails{}, err
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return domain.ConversationDetails{}, err
	}
	status, err := e.Connection(ctx, c.AccountID)
	if err != nil {
		return domain.ConversationDetails{}, err
	}
	result := domain.ConversationDetails{ConversationID: c.ID, AccountID: a.ID, Kind: c.Kind, Address: c.Address, AccountConnection: status, ConversationInfo: domain.EmptyConversationInfo(c.Kind, "not_connected")}
	if status.State != "connected" {
		return result, nil
	}
	result.ConversationInfo = domain.EmptyConversationInfo(c.Kind, "unsupported")
	if provider, ok := e.protocols.(DetailProvider); ok {
		info, err := provider.ConversationInfo(ctx, a, c)
		if err != nil {
			return result, err
		}
		result.ConversationInfo = info
		if info.State == "not_connected" {
			current, err := e.Connection(ctx, a.ID)
			if err != nil {
				return result, err
			}
			result.AccountConnection = current
		}
	}
	return result, nil
}
