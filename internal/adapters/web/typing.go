package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) typing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/ui/api/v1/conversations/"), "/typing")
	engine, account := r.Header.Get("X-Oie-Engine-ID"), r.Header.Get("X-Oie-Account-ID")
	if !validOperationKey(id) || !validOperationKey(engine) || !validOperationKey(account) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodPut && !validCSRF(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if current.JSON200.Id != engine || current.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		w.WriteHeader(http.StatusConflict)
		return
	}
	owner, err := s.client.GetConversationWithResponse(ctx, id)
	if err != nil || owner.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if owner.JSON200.AccountId != account {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if r.Method == http.MethodGet {
		status, err := s.client.GetConversationTypingWithResponse(ctx, id)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if status.JSON200 == nil {
			w.WriteHeader(status.StatusCode())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(status.JSON200)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
	decoder.DisallowUnknownFields()
	var input struct {
		Active *bool `json:"active"`
	}
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Active == nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	status, err := s.client.SetConversationTypingWithResponse(ctx, id, engineapi.TypingState{Active: *input.Active})
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(status.StatusCode())
}
