package web

import (
	"bytes"
	"context"
	_ "embed"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed message-history.js
var messageHistoryScript []byte

//go:embed message-history.css
var messageHistoryStylesheet []byte

const (
	messageHistoryPageSize      = 20
	messageHistoryBodyBytes     = 1 << 20 // Receive accepts up to 1 MiB per retained source.
	messageHistoryMetadataBytes = 1 << 20
)

type historyItemView struct {
	engineapi.MessageEditHistoryItem
	Formatted templ.Component
}

type historyPageView struct {
	Page                     engineapi.MessageEditHistoryPage
	Items                    []historyItemView
	EngineID, Cursor, Locale string
}

func (v view) editHistoryHref(m messageView) string {
	return historyHref(v.EngineID, m.AccountId, m.ConversationId, m.Id, "", v.Locale)
}
func historyHref(engine, account, conversation, message, cursor, locale string) string {
	q := url.Values{"engineId": {engine}, "accountId": {account}, "conversationId": {conversation}, "messageId": {message}, "lang": {locale}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return "/ui/message-history?" + q.Encode()
}
func (h historyPageView) nextHref() string {
	if h.Page.NextCursor == nil || *h.Page.NextCursor == "" {
		return ""
	}
	return historyHref(h.EngineID, h.Page.AccountId, h.Page.ConversationId, h.Page.RequestedMessageId, *h.Page.NextCursor, h.Locale)
}
func validHistorySnapshot(snapshot string) bool {
	return len(snapshot) == 64 && strings.Trim(snapshot, "0123456789abcdef") == ""
}
func validHistoryPage(p engineapi.MessageEditHistoryPage, source engineapi.Message, requested, account, conversation string) bool {
	if p.RequestedMessageId != requested || p.MessageId != source.Id || p.AccountId != account || p.ConversationId != conversation || !validOperationKey(p.CurrentRevisionId) || !validHistorySnapshot(p.Snapshot) || len(p.Items) > messageHistoryPageSize {
		return false
	}
	if p.NextCursor != nil && (*p.NextCursor == "" || len(*p.NextCursor) > 2048 || !utf8.ValidString(*p.NextCursor)) {
		return false
	}
	seen := map[string]bool{}
	// Text has its own per-source bound. A full page of valid 1 MiB sources
	// must remain readable; a smaller aggregate bound would hide retained edits.
	metadataSize := 0
	for _, item := range p.Items {
		if !validOperationKey(item.RevisionId) || !validOperationKey(item.SourceMessageId) || !validOperationKey(item.Sender.Id) || item.CreatedAt.IsZero() || seen[item.RevisionId] || item.IsCurrent != (item.RevisionId == p.CurrentRevisionId) || len(item.ParentRevisionIds) > 128 || len(item.OriginalText) > messageHistoryBodyBytes || !utf8.ValidString(item.OriginalText) || !utf8.ValidString(item.Sender.DisplayName) {
			return false
		}
		seen[item.RevisionId] = true
		metadataSize += len(item.RevisionId) + len(item.SourceMessageId) + len(item.Sender.Id) + len(item.Sender.DisplayName)
		if metadataSize > messageHistoryMetadataBytes {
			return false
		}
		for _, parent := range item.ParentRevisionIds {
			if !validOperationKey(parent) {
				return false
			}
			metadataSize += len(parent)
			if metadataSize > messageHistoryMetadataBytes {
				return false
			}
		}
	}
	return true
}

// History reads only canonical retained records. It deliberately does not load
// profile, room, preview, media or read-state projections while browsing versions.
func (s *Server) messageEditHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	v := s.baseView(r)
	q := r.URL.Query()
	engine, account, conversation, message, cursor := q.Get("engineId"), q.Get("accountId"), q.Get("conversationId"), q.Get("messageId"), q.Get("cursor")
	fail := func(status int) {
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_ = messageHistoryError(v, status == 409 || status == 400).Render(presentationContext(r.Context(), v), w)
		}
	}
	for _, id := range []string{engine, account, conversation, message} {
		if !validOperationKey(id) {
			fail(400)
			return
		}
	}
	if len(cursor) > 2048 || !utf8.ValidString(cursor) {
		fail(400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		fail(503)
		return
	}
	if current.JSON200.Id != engine {
		fail(409)
		return
	}
	source, err := s.client.GetMessageWithResponse(ctx, message)
	if err != nil {
		fail(503)
		return
	}
	if source.StatusCode() == 404 {
		fail(404)
		return
	}
	if source.JSON200 == nil {
		fail(503)
		return
	}
	m := source.JSON200
	if !validOperationKey(m.Id) || m.AccountId != account || m.ConversationId != conversation {
		fail(404)
		return
	}
	limit := messageHistoryPageSize
	params := &engineapi.GetMessageEditHistoryParams{Limit: &limit}
	if cursor != "" {
		params.Cursor = &cursor
	}
	response, err := s.client.GetMessageEditHistoryWithResponse(ctx, message, params)
	if err != nil {
		fail(503)
		return
	}
	if response.StatusCode() == 400 || response.StatusCode() == 404 || response.StatusCode() == 409 {
		fail(response.StatusCode())
		return
	}
	if response.JSON200 == nil {
		fail(503)
		return
	}
	page := *response.JSON200
	if !validHistoryPage(page, *m, message, account, conversation) {
		fail(503)
		return
	}
	h := historyPageView{Page: page, EngineID: engine, Cursor: cursor, Locale: v.Locale}
	// The bounded engine read also limits optional formatting. Render the
	// validated page under the request context so exhausted formatting time
	// leaves exact plain-text versions available.
	formatted := make(map[string]templ.Component, len(page.Items))
	for _, item := range page.Items {
		content, exists := formatted[item.OriginalText]
		if !exists && ctx.Err() == nil {
			content = formattedMessage(ctx, item.OriginalText)
			formatted[item.OriginalText] = content
		}
		h.Items = append(h.Items, historyItemView{MessageEditHistoryItem: item, Formatted: content})
	}
	var out bytes.Buffer
	component := messageHistoryPage(v, h)
	if r.Header.Get("X-Oie-Fragment") != "message-history" {
		component = messageHistoryDocument(v, h)
	}
	if err := component.Render(presentationContext(r.Context(), v), &out); err != nil {
		fail(503)
		return
	}
	if r.Method != http.MethodHead {
		_, _ = out.WriteTo(w)
	}
}
