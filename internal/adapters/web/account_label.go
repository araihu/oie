package web

import (
	"context"
	"net/http"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) updateAccountLabel(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id, revision := r.PostForm.Get("accountId"), r.PostForm.Get("expectedRevisionId")
	if !validOperationKey(id) || !validOperationKey(revision) || len(r.PostForm["label"]) != 1 {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	failed := mutationResult{Message: text.AccountRenameFailed}
	response, err := s.client.SetAccountLabelWithResponse(ctx, id, engineapi.SetAccountLabel{
		Label: r.PostForm.Get("label"), ExpectedRevisionId: revision,
	})
	if err != nil {
		return http.StatusServiceUnavailable, failed
	}
	switch status := response.StatusCode(); status {
	case http.StatusOK:
		if response.JSON200 != nil && response.JSON200.Id == id {
			return http.StatusOK, mutationResult{OK: true, Message: text.AccountRenameSaved, Account: response.JSON200}
		}
	case http.StatusBadRequest:
		return status, mutationResult{Message: text.AccountRenameInvalid, Rejected: true}
	case http.StatusNotFound:
		return status, mutationResult{Message: text.AccountMissing, Rejected: true}
	case http.StatusConflict:
		if current, err := s.client.GetAccountWithResponse(ctx, id); err == nil && current.JSON200 != nil && current.JSON200.Id == id {
			return status, mutationResult{Message: text.AccountRenameConflict, Account: current.JSON200, Rejected: true}
		}
		return status, mutationResult{Message: text.AccountRenameConflict, Rejected: true}
	}
	return http.StatusServiceUnavailable, failed
}
