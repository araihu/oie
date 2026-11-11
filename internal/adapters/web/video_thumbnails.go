package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) videoThumbnailStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/ui/api/v1/shared-content/"), "/thumbnail")
	if r.Method != http.MethodGet || !validOperationKey(id) || r.URL.RawQuery != "" {
		previewStatusProblem(w, 400, "invalid_request")
		return
	}
	engine := r.Header.Get("X-Oie-Engine-ID")
	if len(r.Header.Values("X-Oie-Engine-ID")) != 1 || !validOperationKey(engine) {
		previewStatusProblem(w, 400, "invalid_request")
		return
	}
	current, err := s.client.GetEngineWithResponse(r.Context())
	if err != nil || current.JSON200 == nil {
		previewStatusProblem(w, 503, "unavailable")
		return
	}
	if current.JSON200.Id != engine {
		previewStatusProblem(w, 409, "engine_changed")
		return
	}
	response, err := s.client.GetVideoThumbnailWithResponse(r.Context(), id)
	if err != nil || len(response.Body) > 32<<10 {
		previewStatusProblem(w, 503, "unavailable")
		return
	}
	if response.JSON200 != nil && validVideoThumbnailStatus(*response.JSON200, id) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response.JSON200)
		return
	}
	status := response.StatusCode()
	if status != 400 && status != 404 && status != 503 {
		status = 503
	}
	previewStatusProblem(w, status, "unavailable")
}
func validVideoThumbnailStatus(p engineapi.VideoThumbnail, id string) bool {
	for _, value := range []string{p.Id, p.RevisionId, p.SharedContentId, p.AccountId, p.ConversationId, p.MessageId} {
		if !validOperationKey(value) {
			return false
		}
	}
	if p.Kind != "video" || p.SharedContentId != id || p.UpdatedAt.IsZero() || len(p.Url) > 4096 || safePreviewURL(p.Url) == "" {
		return false
	}
	if p.State != "not_requested" && p.State != "queued" && p.State != "ready" && p.State != "unavailable" {
		return false
	}
	if p.ImageHash != nil && !validAvatarHash(*p.ImageHash) {
		return false
	}
	return p.LastErrorCode == nil || utf8.ValidString(*p.LastErrorCode) && utf8.RuneCountInString(*p.LastErrorCode) <= 40
}
func (s *Server) requestVideoThumbnail(ctx context.Context, r *http.Request, text copy) (int, mutationResult) {
	id := r.Form.Get("itemId")
	if !validOperationKey(id) {
		return 422, mutationResult{Message: text.InvalidForm, Rejected: true}
	}
	response, err := s.client.RequestVideoThumbnailWithResponse(ctx, id)
	if err != nil {
		return 503, mutationResult{Message: text.VideoThumbnailFailed}
	}
	p := response.JSON200
	if p == nil {
		p = response.JSON202
	}
	if p == nil || !validVideoThumbnailStatus(*p, id) {
		status := response.StatusCode()
		if status != 400 && status != 404 && status != 409 {
			status = 503
		}
		return status, mutationResult{Message: text.VideoThumbnailFailed}
	}
	return response.StatusCode(), mutationResult{OK: true, Thumbnail: p}
}
