package web

import (
	"net/http"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) roomOmemoPage(w http.ResponseWriter, r *http.Request, v view, account, chat string) {
	ctx := r.Context()
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.ParseForm() != nil || !validCSRF(r) {
			http.Error(w, v.Text.CSRFError, http.StatusForbidden)
			return
		}
		if r.PostForm.Get("engineId") != v.EngineID {
			http.Error(w, v.Text.OperationConflict, http.StatusConflict)
			return
		}
		action := r.PostForm.Get("action")
		if action != "encrypt" && action != "plaintext" {
			http.Error(w, v.Text.InvalidForm, http.StatusBadRequest)
			return
		}
		saved, err := s.client.SetRoomOmemoWithResponse(ctx, chat, engineapi.SetRoomOmemoJSONRequestBody{Enabled: action == "encrypt"})
		if err != nil || saved.StatusCode() != http.StatusNoContent {
			http.Error(w, v.Text.OmemoRoomUnavailable, http.StatusConflict)
			return
		}
		http.Redirect(w, r, v.omemoHref(account, chat), http.StatusSeeOther)
		return
	}
	result, err := s.client.GetRoomOmemoWithResponse(ctx, chat)
	if err != nil || result.JSON200 == nil {
		http.Error(w, v.Text.OmemoUnavailable, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = roomOmemoSettings(v, *result.JSON200, account, chat).Render(ctx, w)
}
