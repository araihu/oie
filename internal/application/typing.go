package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

type TypingProtocol interface {
	Typing(domain.Conversation) bool
	SetTyping(context.Context, domain.Conversation, bool)
}

func (e *Engine) Typing(ctx context.Context, conversationID domain.ID) (bool, error) {
	conversation, err := e.typingConversation(ctx, conversationID)
	if err != nil {
		return false, err
	}
	protocol, ok := e.protocols.(TypingProtocol)
	if !ok {
		return false, ErrProtocolUnavailable
	}
	return protocol.Typing(conversation), nil
}

func (e *Engine) SetTyping(ctx context.Context, conversationID domain.ID, active bool) error {
	conversation, err := e.typingConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	protocol, ok := e.protocols.(TypingProtocol)
	if !ok {
		return ErrProtocolUnavailable
	}
	preferences, err := e.AccountCommunicationPreferences(ctx, conversation.AccountID)
	if err != nil {
		return err
	}
	if preferences.SendTyping {
		protocol.SetTyping(ctx, conversation, active)
	}
	return nil
}

func (e *Engine) typingConversation(ctx context.Context, id domain.ID) (domain.Conversation, error) {
	conversation, err := e.Conversation(ctx, id)
	if err == nil && (conversation.Kind != "direct" || conversation.Request != "") {
		err = ErrStateConflict
	}
	return conversation, err
}
