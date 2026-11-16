package web

import (
	"context"
	_ "embed"
	"net/http"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed message-retractions.js
var messageRetractionsScript []byte

func (s *Server) submitRetraction(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id, revision := r.Form.Get("messageId"), r.Form.Get("revisionId")
	if !validOperationKey(id) || !validOperationKey(revision) {
		return http.StatusBadRequest, mutationResult{Message: text.InvalidForm}
	}
	response, err := s.client.RetractMessageWithResponse(ctx, id,
		engineapi.RetractMessage{ExpectedRevisionId: revision})
	if err == nil {
		if m := response.JSON200; m != nil && m.Id == id && m.IsOwn && m.Retraction != nil {
			return http.StatusOK, mutationResult{OK: true, Message: text.RetractionRequested, MessageID: id}
		}
		if response.StatusCode() >= 400 && response.StatusCode() < 500 {
			return response.StatusCode(), mutationResult{Message: text.RetractionFailed}
		}
	}
	return http.StatusServiceUnavailable, mutationResult{Message: text.RetractionUnconfirmed}
}
