package web

import (
	"context"
	_ "embed"
	"net/http"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed new-conversation.js
var conversationPickerScript []byte

func (s *Server) conversationPickerResults(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	params := r.URL.Query()
	query := strings.TrimSpace(params.Get("term"))
	cursor := params.Get("cursor")
	group := params.Get("group")
	var account *string
	if id := params.Get("account"); id != "" {
		account = &id
	}
	if (account != nil && !validOperationKey(*account)) || len([]rune(query)) > 200 || len(cursor) > 4096 || (group != "chats" && group != "contacts") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var next *string
	if cursor != "" {
		next = &cursor
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	v := s.baseView(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if group == "chats" {
		limit := 24
		if query == "" {
			limit = 5
		}
		chats, err := s.client.ListConversationsWithResponse(ctx, &engineapi.ListConversationsParams{AccountId: account, Q: &query, Limit: &limit, Cursor: next})
		if err != nil || chats.JSON200 == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = pickerChats(v, *chats.JSON200, query != "").Render(ctx, w)
		return
	}
	limit := 24
	filter := &engineapi.ListContactsParams{AccountId: account, Q: &query, Limit: &limit, Cursor: next}
	page, err := s.client.ListContactsWithResponse(ctx, filter)
	if err != nil || page.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	_ = pickerContacts(v, *page.JSON200).Render(ctx, w)
}

// An unavailable roster can still return contacts from its last snapshot.
func pickerContactCoverage(text copy, page engineapi.ContactPage) string {
	known := 0
	for _, source := range page.Sources {
		if source.State == "known" {
			known++
		}
	}
	if known == len(page.Sources) {
		return ""
	}
	if known == 0 && len(page.Items) == 0 {
		return text.Unavailable
	}
	return text.SearchContactsPartial
}
