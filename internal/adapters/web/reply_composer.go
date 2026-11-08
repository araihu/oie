package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

type replyPreparation struct {
	EngineID       string `json:"engineId"`
	MessageID      string `json:"messageId"`
	AccountID      string `json:"accountId"`
	ConversationID string `json:"conversationId"`
	SenderName     string `json:"senderName"`
	Excerpt        string `json:"excerpt"`
}

func quoteExcerpt(m engineapi.Message) string {
	text := m.OriginalText
	if m.Reply != nil {
		text = m.Reply.BodyText
	}
	runes := []rune(text)
	if len(runes) > 512 {
		return string(runes[:511]) + "…"
	}
	return text
}
func (s *Server) retainedReplySource(ctx context.Context, conversation, account, target string) (*engineapi.Message, int) {
	limit := 1
	response, err := s.client.GetMessageContextWithResponse(ctx, conversation, target, &engineapi.GetMessageContextParams{Limit: &limit})
	if err != nil {
		return nil, 503
	}
	if response.JSON200 == nil {
		status := response.StatusCode()
		if status != 400 && status != 404 {
			status = 503
		}
		return nil, status
	}
	if response.JSON200.TargetId != target {
		return nil, 503
	}
	for _, m := range response.JSON200.Items {
		if m.Id == target && m.ConversationId == conversation && m.AccountId == account {
			return &m, 200
		}
	}
	return nil, 503
}
func (s *Server) prepareReply(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	text := s.catalogs[locale(r)]
	q := r.URL.Query()
	conversation, account, target := q.Get("conversationId"), q.Get("accountId"), q.Get("messageId")
	fail := func(status int, message, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(mutationResult{Message: message, Code: code, Rejected: status == 400 || status == 404})
	}
	if !validOperationKey(conversation) || !validOperationKey(account) || !validOperationKey(target) {
		fail(400, text.ReplyUnavailable, "invalid_request")
		return
	}
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil {
		fail(503, text.UnavailableBody, "unavailable")
		return
	}
	if q.Get("engineId") != engine.JSON200.Id || engine.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		fail(409, text.EngineChanged, "engine_changed")
		return
	}
	owner, err := s.client.GetConversationWithResponse(ctx, conversation)
	if err != nil || owner.JSON200 == nil {
		fail(503, text.ReplyUnavailable, "unavailable")
		return
	}
	if owner.JSON200.AccountId != account {
		fail(404, text.ReplyUnavailable, "not_found")
		return
	}
	source, status := s.retainedReplySource(ctx, conversation, account, target)
	if source == nil {
		fail(status, text.ReplyUnavailable, "not_found")
		return
	}
	_ = json.NewEncoder(w).Encode(replyPreparation{EngineID: engine.JSON200.Id, MessageID: source.Id, AccountID: source.AccountId, ConversationID: source.ConversationId, SenderName: source.Sender.DisplayName, Excerpt: quoteExcerpt(*source)})
}
func matchingSend(m engineapi.Message, conversation, account, body, target string) bool {
	if m.ConversationId != conversation || m.AccountId != account {
		return false
	}
	if target == "" {
		return m.ReplyToMessageId == nil && m.OriginalText == body
	}
	return m.ReplyToMessageId != nil && *m.ReplyToMessageId == target && m.Reply != nil && m.Reply.BodyText == body
}

// A missing receipt is different from an unreadable receipt. Never label an
// uncertain operation rejected just because a later room/source read changed.
func (s *Server) sendReceipt(ctx context.Context, key string) (*engineapi.Message, string) {
	receipt, err := s.client.GetOperationWithResponse(ctx, key)
	if err != nil {
		return nil, "unknown"
	}
	if receipt.StatusCode() == 404 {
		return nil, "missing"
	}
	if receipt.JSON200 == nil {
		return nil, "unknown"
	}
	if receipt.JSON200.Kind != "send_message" {
		return nil, "conflict"
	}
	response, err := s.client.GetMessageWithResponse(ctx, receipt.JSON200.ResourceId)
	if err != nil || response.JSON200 == nil {
		return nil, "unknown"
	}
	return response.JSON200, "found"
}
