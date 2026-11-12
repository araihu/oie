package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

type replyPreparation struct {
	EngineID           string `json:"engineId"`
	MessageID          string `json:"messageId"`
	RequestedMessageID string `json:"requestedMessageId"`
	RevisionID         string `json:"revisionId"`
	AccountID          string `json:"accountId"`
	ConversationID     string `json:"conversationId"`
	SenderName         string `json:"senderName"`
	Excerpt            string `json:"excerpt"`
	ExcerptSHA256      string `json:"excerptSha256"`
}

func quoteExcerpt(m engineapi.Message) string {
	text := m.OriginalText
	if m.Reply != nil {
		text = m.Reply.BodyText
	}
	runes := []rune(text)
	if len(runes) > 512 {
		return string(runes[:512])
	}
	return text
}

func validReplyExcerptSHA256(hash string) bool {
	if len(hash) != sha256.Size*2 || strings.ToLower(hash) != hash {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
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
	if !validContextTarget(*response.JSON200, target, conversation, account) {
		return nil, 503
	}
	for _, m := range response.JSON200.Items {
		if m.Id == response.JSON200.TargetId && m.ConversationId == conversation && m.AccountId == account {
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
	excerpt := quoteExcerpt(*source)
	digest := sha256.Sum256([]byte(excerpt))
	_ = json.NewEncoder(w).Encode(replyPreparation{
		EngineID: engine.JSON200.Id, AccountID: source.AccountId, ConversationID: source.ConversationId,
		MessageID: source.Id, RequestedMessageID: target, RevisionID: source.RevisionId,
		SenderName: source.Sender.DisplayName, Excerpt: excerpt, ExcerptSHA256: hex.EncodeToString(digest[:]),
	})
}
func matchingSend(m engineapi.Message, conversation, account, body, target string, selected ...[]engineapi.MentionSelection) bool {
	var mentions []engineapi.MentionSelection
	if len(selected) > 0 {
		mentions = selected[0]
	}
	if !matchingMentionSelections(m, mentions) {
		return false
	}
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
	include := true
	receipt, err := s.client.GetOperationWithResponse(ctx, key, &engineapi.GetOperationParams{IncludeAcceptedMessage: &include})
	if err != nil {
		return nil, "unknown"
	}
	if receipt.StatusCode() == 404 {
		return nil, "missing"
	}
	if receipt.JSON200 == nil || receipt.JSON200.Id != key || receipt.JSON200.Status != "succeeded" || !validOperationKey(receipt.JSON200.ResourceId) {
		return nil, "unknown"
	}
	if receipt.JSON200.Kind != "send_message" {
		return nil, "conflict"
	}
	accepted := receipt.JSON200.AcceptedMessage
	if accepted == nil {
		return nil, "legacy"
	}
	if accepted.Id != receipt.JSON200.ResourceId || !accepted.IsOwn || accepted.SendState != "queued" {
		return nil, "unknown"
	}
	return accepted, "found"
}
