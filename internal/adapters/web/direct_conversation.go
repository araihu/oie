package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"mellium.im/xmpp/jid"
)

func (s *Server) findDirectConversation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	params := r.URL.Query()
	account, address := params.Get("account"), strings.TrimSpace(params.Get("address"))
	peer, err := jid.Parse(address)
	if !validOperationKey(account) || len(address) > 1023 || err != nil || peer.Localpart() == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	address = peer.Bare().String()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	kind := engineapi.ListConversationsParamsKindDirect
	limit := 100
	for _, archived := range []bool{false, true} {
		filter := engineapi.ListConversationsParams{AccountId: &account, Kind: &kind, Q: &address, Limit: &limit, Archived: &archived}
		for {
			page, err := s.client.ListConversationsWithResponse(ctx, &filter)
			if err != nil || page.JSON200 == nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			for _, chat := range page.JSON200.Items {
				if chat.Kind == "direct" && chat.AccountId == account && chat.Address == address {
					v := s.baseView(r)
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					_, _ = w.Write([]byte(v.href("section", "chats", "account", account, "chat", chat.Id, "lang", v.Locale, "theme", v.Theme)))
					return
				}
			}
			if page.JSON200.NextCursor == nil {
				break
			}
			filter.Cursor = page.JSON200.NextCursor
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
