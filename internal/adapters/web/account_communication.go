package web

import (
	"context"
	"net/http"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) changeAccountCommunication(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	account, revision := r.Form.Get("accountId"), r.Form.Get("expectedRevisionId")
	presence := engineapi.AccountCommunicationPreferencesPresence(r.Form.Get("presence"))
	if !validOperationKey(account) || revision != "" && !validOperationKey(revision) || !presence.Valid() {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm}
	}
	input := engineapi.AccountCommunicationPreferences{Presence: presence}
	if r.Form.Has("sendTypingField") {
		input.SendTyping = new(r.Form.Get("sendTyping") == "on")
	}
	if r.Form.Has("sendReadReceiptsField") {
		input.SendReadReceipts = new(r.Form.Get("sendReadReceipts") == "on")
	}
	if revision != "" {
		input.RevisionId = &revision
	}
	saved, err := s.client.SetAccountCommunicationPreferencesWithResponse(ctx, account, input)
	if err == nil && saved.JSON200 != nil {
		return http.StatusOK, mutationResult{OK: true, Message: text.AccountPresenceSaved, Communication: saved.JSON200}
	}
	if err == nil && saved.StatusCode() == http.StatusConflict {
		return http.StatusConflict, mutationResult{Code: "revision_conflict", Message: text.AccountPresenceConflict}
	}
	return http.StatusServiceUnavailable, mutationResult{Message: text.AccountPresenceFailed}
}
