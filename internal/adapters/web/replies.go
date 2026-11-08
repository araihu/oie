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
func (s *Server) contextMessages(ctx context.Context, v *view, account string) ([]engineapi.Message, bool) {
	if v.FocusMessage == "" {
		return nil, false
	}
	if !validOperationKey(v.FocusMessage) {
		v.ContextMissing = true
		return nil, false
	}
	limit := 50
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
	page := response.JSON200
	target := false
	if page.TargetId != v.FocusMessage {
		v.Unavailable = true
		return nil, true
	}
	for _, m := range page.Items {
		if m.ConversationId != v.ChatID || m.AccountId != account {
			v.Unavailable = true
			return nil, true
		}
		if m.Id == v.FocusMessage {
			target = true
		}
	}
	if !target {
		v.Unavailable = true
		return nil, true
	}
	v.ContextHasNewer = page.HasNewer
	v.ContextHasOlder = page.HasOlder
	return page.Items, true
}
