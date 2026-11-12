package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed draft-link-preview.js
var draftLinkPreviewScript []byte

//go:embed draft-link-preview.css
var draftLinkPreviewStylesheet []byte

func (s *Server) resolveDraftLinkPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !validCSRF(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	engineID := r.Header.Get("X-Oie-Engine-ID")
	if kind != "application/json" || !validOperationKey(engineID) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var input engineapi.DraftLinkPreviewRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF ||
		len(input.Url) > 4096 || safePreviewURL(input.Url) == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if current.JSON200.Id != engineID || current.HTTPResponse.Header.Get("X-Oie-Mock") == "true" {
		w.WriteHeader(http.StatusConflict)
		return
	}
	result, err := s.archiveClient.ResolveDraftLinkPreviewWithResponse(ctx, input)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if result.JSON200 != nil && result.JSON200.Url == input.Url {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result.JSON200)
		return
	}
	switch result.StatusCode() {
	case http.StatusNoContent, http.StatusNotFound:
		w.WriteHeader(http.StatusNoContent)
	case http.StatusBadRequest:
		w.WriteHeader(http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}
