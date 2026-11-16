package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (v view) contactsHref(account, cursor string) string {
	return v.href("open", "accounts", "accountPage", "contacts", "managedAccount", account, "contactCursor", cursor)
}

func (s *Server) loadContacts(ctx context.Context, r *http.Request, v *view) {
	account := v.managedAccountID()
	if v.Preview || v.account(account).Address == "" {
		return
	}
	limit := 30
	params := &engineapi.ListContactsParams{AccountId: &account, Limit: &limit, IncludePresence: new(true)}
	if cursor := r.URL.Query().Get("contactCursor"); cursor != "" {
		params.Cursor = &cursor
	}
	page, err := s.client.ListContactsWithResponse(ctx, params)
	if err == nil && page.JSON200 != nil {
		v.Contacts = page.JSON200
	}
	requests, err := s.client.ListContactRequestsWithResponse(ctx, account)
	if err == nil && requests.JSON200 != nil {
		v.ContactRequests = requests.JSON200
	}
}

func (s *Server) changeContact(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	account, address := r.Form.Get("accountId"), strings.TrimSpace(r.Form.Get("address"))
	if !validOperationKey(account) || address == "" || len(address) > 1023 {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm}
	}
	input := engineapi.ContactAction{Address: address, Action: engineapi.ContactActionAction(r.Form.Get("contactAction"))}
	response, err := s.client.ChangeProtocolContactWithResponse(ctx, account, input)
	if err != nil {
		return http.StatusServiceUnavailable, mutationResult{Message: text.ContactChangeFailed}
	}
	if response.StatusCode() != http.StatusNoContent {
		return response.StatusCode(), mutationResult{Message: text.ContactChangeFailed}
	}
	return http.StatusOK, mutationResult{OK: true, Message: text.ContactChangeSaved}
}
