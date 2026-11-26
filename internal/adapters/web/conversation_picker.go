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
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	v := s.baseView(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if group == "chats" {
		limit := 5
		chats, err := s.client.ListConversationsWithResponse(ctx, &engineapi.ListConversationsParams{AccountId: account, Q: &query, Limit: &limit})
		if err != nil || chats.JSON200 == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = pickerChats(v, chats.JSON200.Items).Render(ctx, w)
		return
	}
	limit := 24
	filter := &engineapi.ListContactsParams{AccountId: account, Q: &query, Limit: &limit}
	if cursor != "" {
		filter.Cursor = &cursor
	}
	page, err := s.client.ListContactsWithResponse(ctx, filter)
	if err != nil || page.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	_ = pickerContacts(v, *page.JSON200).Render(ctx, w)
}
