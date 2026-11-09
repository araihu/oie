package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// previewStatus exposes the existing engine GET operation through the BFF. It
// reads retained metadata only; it never ensures, refreshes or downloads media.
func (s *Server) previewStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		previewStatusProblem(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/previews/")
	if !validOperationKey(id) {
		previewStatusProblem(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if values := r.Header.Values("X-Oie-Engine-ID"); len(values) > 0 {
		if len(values) != 1 || !validOperationKey(values[0]) {
			previewStatusProblem(w, http.StatusBadRequest, "invalid_request")
			return
		}
		current, err := s.client.GetEngineWithResponse(r.Context())
		if err != nil || current.JSON200 == nil || !validOperationKey(current.JSON200.Id) {
			previewStatusProblem(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		if current.JSON200.Id != values[0] {
			previewStatusProblem(w, http.StatusConflict, "engine_changed")
			return
		}
	}
	result, err := s.client.GetMessagePreviewWithResponse(r.Context(), id)
	if err != nil || len(result.Body) > 32<<10 {
		previewStatusProblem(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if result.JSON200 != nil && validPreviewStatus(*result.JSON200, id) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result.JSON200)
		return
	}
	var problem *engineapi.Problem
	switch result.StatusCode() {
	case http.StatusBadRequest:
		problem = result.ApplicationproblemJSON400
	case http.StatusNotFound:
		problem = result.ApplicationproblemJSON404
	case http.StatusServiceUnavailable:
		problem = result.ApplicationproblemJSON503
	}
	if problem != nil && problem.Status == result.StatusCode() {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(problem.Status)
		_ = json.NewEncoder(w).Encode(problem)
		return
	}
	previewStatusProblem(w, http.StatusServiceUnavailable, "unavailable")
}
func previewStatusProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(engineapi.Problem{Status: status, Title: http.StatusText(status), Code: engineapi.ProblemCode(code)})
}
func validPreviewStatus(p engineapi.MessagePreview, id string) bool {
	if p.Id != id || !validOperationKey(p.RevisionId) || !validOperationKey(p.AccountId) || !validOperationKey(p.ConversationId) || !validOperationKey(p.MessageId) || p.UpdatedAt.IsZero() || len(p.Url) > 4096 || safePreviewURL(p.Url) == "" {
		return false
	}
	if p.Kind != "image" && p.Kind != "link" {
		return false
	}
	if p.State != "queued" && p.State != "ready" && p.State != "unavailable" {
		return false
	}
	if p.DownloadState != "not_downloaded" && p.DownloadState != "queued" && p.DownloadState != "downloading" && p.DownloadState != "ready" && p.DownloadState != "failed" {
		return false
	}
	for _, field := range []struct {
		value *string
		limit int
	}{{p.Title, 300}, {p.Description, 1000}, {p.SiteName, 200}, {p.LastErrorCode, 40}} {
		if field.value != nil && (!utf8.ValidString(*field.value) || utf8.RuneCountInString(*field.value) > field.limit) {
			return false
		}
	}
	for _, hash := range []*string{p.ImageHash, p.FaviconHash, p.FileHash} {
		if hash != nil && !validAvatarHash(*hash) {
			return false
		}
	}
	if p.FileMimeType != nil && *p.FileMimeType != "image/png" && *p.FileMimeType != "image/jpeg" && *p.FileMimeType != "image/gif" {
		return false
	}
	if p.FileSize != nil && (*p.FileSize < 1 || *p.FileSize > 32<<20) {
		return false
	}
	return p.Integrity == nil || *p.Integrity == engineapi.ComputedOnly
}
