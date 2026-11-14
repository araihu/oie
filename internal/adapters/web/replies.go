package web

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (m messageView) displayBody() string {
	if m.Reply != nil {
		return m.Reply.BodyText
	}
	return m.OriginalText
}
func replyNavigable(reply *engineapi.MessageReply) bool {
	return reply != nil && reply.Available && reply.MessageId != nil && validOperationKey(*reply.MessageId)
}
func (v view) replySender(reply *engineapi.MessageReply) string {
	if reply != nil && reply.SenderName != nil && *reply.SenderName != "" {
		return *reply.SenderName
	}
	return v.Text.UnknownOriginal
}
func (v view) replyHref(m messageView) string {
	if !replyNavigable(m.Reply) {
		return ""
	}
	return v.href("chat", m.ConversationId, "message", *m.Reply.MessageId, "lang", v.Locale, "theme", v.Theme)
}
func (v view) latestHref() string {
	return v.href("chat", v.ChatID, "message", "", "lang", v.Locale, "theme", v.Theme)
}

// Context is bounded and account/conversation-scoped by the canonical engine.
// Validate the projection before exposing it, including the requested target.
func (s *Server) contextMessages(ctx context.Context, v *view, account string, limit int) ([]engineapi.Message, bool) {
	if v.FocusMessage == "" {
		return nil, false
	}
	if !validOperationKey(v.FocusMessage) {
		v.ContextMissing = true
		return nil, false
	}
	response, err := s.client.GetMessageContextWithResponse(ctx, v.ChatID, v.FocusMessage, &engineapi.GetMessageContextParams{Limit: &limit})
	if err != nil {
		v.Unavailable = true
		return nil, true
	}
	if response.StatusCode() == 404 || response.StatusCode() == 400 {
		v.ContextMissing = true
		return nil, false
	}
	if response.JSON200 == nil {
		v.Unavailable = true
		return nil, true
	}
	if !v.applyMessageContext(*response.JSON200, account) {
		v.Unavailable = true
		return nil, true
	}
	return response.JSON200.Items, true
}

func (v *view) applyMessageContext(page engineapi.MessageContext, account string) bool {
	if !validContextTarget(page, v.FocusMessage, v.ChatID, account) {
		return false
	}
	v.FocusMessage = page.TargetId
	v.ContextHasNewer = page.HasNewer
	v.ContextHasOlder = page.HasOlder
	return true
}

// An alias can change only the canonical target, never its ownership. The engine
// must explicitly prove the exact requested UUID and include the canonical row.
func validContextTarget(page engineapi.MessageContext, requested, conversation, account string) bool {
	if !validOperationKey(page.TargetId) || !validOperationKey(requested) {
		return false
	}
	if page.TargetId == requested {
		if page.RequestedTargetId != nil {
			return false
		}
	} else if page.RequestedTargetId == nil || *page.RequestedTargetId != requested {
		return false
	}
	target := false
	for _, m := range page.Items {
		if m.ConversationId != conversation || m.AccountId != account || !validOperationKey(m.Id) || !validOperationKey(m.RevisionId) {
			return false
		}
		if m.Id == page.TargetId {
			target = true
		}
	}
	return target
}
