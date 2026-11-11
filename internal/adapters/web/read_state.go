package web

import (
	"context"
	"encoding/json"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"io"
	"net/http"
	"strings"
	"time"
)

// The BFF exposes the engine's read-state contract with its browser CSRF and
// engine/account binding. GET never marks anything read.
func (s *Server) readState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fail := func(status int) {
		if status < 400 || status > 599 {
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(engineapi.Problem{Status: status, Title: http.StatusText(status), Code: engineapi.ProblemCode("invalid_request")})
	}
	if r.Method != "GET" && r.Method != "POST" {
		w.Header().Set("Allow", "GET, POST")
		fail(405)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/ui/api/v1/conversations/"), "/read-state")
	engineID, accountID := r.Header.Get("X-Oie-Engine-ID"), r.Header.Get("X-Oie-Account-ID")
	if !validOperationKey(id) || !validOperationKey(engineID) || !validOperationKey(accountID) {
		fail(400)
		return
	}
	if r.Method == "POST" && !validCSRF(r) {
		fail(403)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		fail(503)
		return
	}
	if current.JSON200.Id != engineID || current.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		fail(409)
		return
	}
	owner, err := s.client.GetConversationWithResponse(ctx, id)
	if err != nil {
		fail(503)
		return
	}
	if owner.JSON200 == nil {
		fail(owner.StatusCode())
		return
	}
	if owner.JSON200.AccountId != accountID {
		fail(409)
		return
	}
	var state *engineapi.ConversationReadState
	if r.Method == "GET" {
		response, callErr := s.client.GetConversationReadStateWithResponse(ctx, id)
		if callErr != nil {
			fail(503)
			return
		}
		if response.JSON200 == nil {
			fail(response.StatusCode())
			return
		}
		state = response.JSON200
	} else {
		key := r.Header.Get("Idempotency-Key")
		if !validOperationKey(key) {
			fail(400)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var body engineapi.MarkMessagesRead
		if err = decoder.Decode(&body); err != nil || len(body.MessageIds) < 1 || len(body.MessageIds) > 100 {
			fail(400)
			return
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			fail(400)
			return
		}
		for _, message := range body.MessageIds {
			if !validOperationKey(message) {
				fail(400)
				return
			}
		}
		response, callErr := s.client.MarkConversationMessagesReadWithResponse(ctx, id, &engineapi.MarkConversationMessagesReadParams{IdempotencyKey: key}, body)
		if callErr != nil {
			fail(503)
			return
		}
		if response.JSON200 == nil {
			fail(response.StatusCode())
			return
		}
		state = response.JSON200
	}
	if state.ConversationId != id || state.AccountId != accountID || state.UnreadCount < 0 {
		fail(503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Oie-Next-Operation-Key", newOperationKey())
	_ = json.NewEncoder(w).Encode(state)
}
