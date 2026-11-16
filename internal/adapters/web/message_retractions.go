package web

import (
	"context"
	_ "embed"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed message-retractions.js
var messageRetractionsScript []byte

func (s *Server) revealRetracted(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	id, engineID, conversation := q.Get("messageId"), q.Get("engineId"), q.Get("conversationId")
	if !validOperationKey(id) || !validOperationKey(engineID) || !validOperationKey(conversation) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if engine.JSON200.Id != engineID {
		w.WriteHeader(http.StatusConflict)
		return
	}
	response, err := s.client.GetRetractedOriginalWithResponse(ctx, id)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	m := response.JSON200
	if m == nil {
		w.WriteHeader(response.StatusCode())
		return
	}
	if m.Id != id || m.ConversationId != conversation || m.Retraction == nil || !m.Retraction.OriginalAvailable {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	v := s.baseView(r)
	v.EngineID = engineID
	v.Selected = &engineapi.Conversation{Id: conversation, AccountId: m.AccountId}
	mv := messageView{Message: *m}
	v.formatMessage(ctx, &mv)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = retractedOriginal(v, mv).Render(presentationContext(ctx, v), w)
}

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
