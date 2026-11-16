package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web/bffapi"
)

// notificationContext reads current archive state and resolves reply ownership
// through the same engine view as the conversation. It also works with engines
// that predate notification sound preferences.
func (s *Server) notificationContext(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	engineID, accountID, conversationID, replyTo := query.Get("engineId"), query.Get("accountId"), query.Get("conversationId"), query.Get("replyTo")
	if !validOperationKey(engineID) || !validOperationKey(accountID) || !validOperationKey(conversationID) ||
		(replyTo != "" && !validOperationKey(replyTo)) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if current.JSON200.Id != engineID {
		w.WriteHeader(http.StatusConflict)
		return
	}
	conversation, err := s.client.GetConversationWithResponse(ctx, conversationID)
	if err != nil || conversation.JSON200 == nil && conversation.StatusCode() != http.StatusNotFound {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	owner := conversation.JSON200
	if owner == nil || owner.Id != conversationID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if owner.AccountId != accountID {
		w.WriteHeader(http.StatusConflict)
		return
	}
	value := bffapi.NotificationContext{Kind: bffapi.NotificationContextKind(owner.Kind), Archived: owner.Archived}
	if !value.Kind.Valid() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	preferences, err := s.client.GetAccountCommunicationPreferencesWithResponse(ctx, accountID)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if preferences.JSON200 != nil {
		value.DoNotDisturb = new(preferences.JSON200.Presence == "dnd")
	} else if preferences.StatusCode() != http.StatusNotFound && preferences.StatusCode() != http.StatusMethodNotAllowed {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if replyTo != "" && owner.Kind == engineapi.ConversationKindRoom {
		if target, err := s.client.GetMessageWithResponse(ctx, replyTo); err == nil && target.JSON200 != nil {
			message := target.JSON200
			value.ReplyToYou = message.Id == replyTo && message.AccountId == accountID &&
				message.ConversationId == conversationID && message.IsOwn
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
