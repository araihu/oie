package web

import (
	"context"
	"net/http"
	"net/url"
)

func (v view) removeAccountHref(id string) string {
	return v.href("open", "accounts", "accountPage", "remove", "managedAccount", id, "lang", v.Locale, "theme", v.Theme)
}

func (s *Server) removeAccount(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id := r.Form.Get("accountId")
	if !validOperationKey(id) || r.Form.Get("confirmation") != "remove" {
		return http.StatusUnprocessableEntity, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	response, err := s.client.RemoveAccountWithResponse(ctx, id)
	if err != nil || response.StatusCode() != http.StatusNoContent {
		return http.StatusServiceUnavailable, mutationResult{Message: text.RemoveAccountFailed}
	}
	destination, _ := url.Parse(accountDestination(r, ""))
	q := destination.Query()
	filterRemoved := q.Get("account") == id
	if filterRemoved {
		q.Del("account")
	}
	if filterRemoved || r.Form.Get("viewAccount") == id {
		for _, key := range []string{"chat", "message", "infoView", "infoType", "infoCursor", "infoQuery", "chatSearch", "chatQuery", "chatSender", "chatFromDate", "chatToDate", "chatIncludeHistory", "chatIncludeRetracted", "chatSearchRevision", "contentConversation", "contentCursor", "favoritesCursor", "scheduleCursor"} {
			q.Del(key)
		}
		q.Set("inbox", "1")
	}
	destination.RawQuery = q.Encode()
	return http.StatusOK, mutationResult{OK: true, Message: text.AccountRemoved, Redirect: destination.String()}
}
