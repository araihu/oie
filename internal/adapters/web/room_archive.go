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

const browserRoomArchivePath = "/ui/room-archive/"

func (s *Server) loadRoomArchive(ctx context.Context, v *view) {
	if v.Preview || v.Selected == nil || v.Selected.Kind != "room" {
		return
	}
	response, err := s.client.GetRoomArchiveWithResponse(ctx, v.Selected.Id)
	if err == nil && response.JSON200 != nil && response.JSON200.ConversationId == v.Selected.Id {
		v.RoomArchive = response.JSON200
	}
}

func decodeArchiveBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst) == nil && decoder.Decode(new(any)) == io.EOF
}

func (s *Server) browserRoomArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.Header().Set("Allow", "POST, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, browserRoomArchivePath)
	engine, account := r.Header.Get("X-Oie-Engine-ID"), r.Header.Get("X-Oie-Account-ID")
	if !validOperationKey(id) || !validOperationKey(engine) || !validOperationKey(account) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !validCSRF(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
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
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if owner.JSON200 == nil || owner.JSON200.Id != id || owner.JSON200.AccountId != account || owner.JSON200.Kind != "room" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPut {
		var body engineapi.SetRoomArchiveMode
		if !decodeArchiveBody(w, r, &body) || !body.Mode.Valid() {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response, err := s.client.SetRoomArchiveModeWithResponse(ctx, id, body)
		if err != nil || response.JSON200 == nil || response.JSON200.ConversationId != id {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(response.JSON200)
		return
	}
	var body engineapi.RoomArchiveRequest
	if !decodeArchiveBody(w, r, &body) || !body.Action.Valid() {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	response, err := s.archiveClient.RequestRoomArchiveWithResponse(ctx, id, body)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if response.JSON200 == nil {
		w.WriteHeader(response.StatusCode())
		return
	}
	result := response.JSON200
	if result.Archive.ConversationId != id || !result.Outcome.Valid() || len(result.Items) > 50 {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	for _, m := range result.Items {
		if m.ConversationId != id || m.AccountId != account || !validOperationKey(m.Id) || !validOperationKey(m.RevisionId) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(result)
}
