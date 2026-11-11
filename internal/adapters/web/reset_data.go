package web

import (
	"context"
	_ "embed"
	"net/http"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed reset-data.js
var resetDataScript []byte

func (s *Server) resetData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !validCSRF(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	id := r.PostForm.Get("engineId")
	confirmation := engineapi.ResetEngineDataJSONBodyConfirmation(r.PostForm.Get("confirmation"))
	if !confirmation.Valid() || !validOperationKey(id) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	response, err := s.archiveClient.ResetEngineDataWithResponse(ctx, engineapi.ResetEngineDataJSONRequestBody{EngineId: id, Confirmation: confirmation})
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if response.StatusCode() != http.StatusNoContent {
		w.WriteHeader(response.StatusCode())
		return
	}
	for _, name := range []string{csrfCookie, "oie_lang", "oie_theme"} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	}
	w.Header().Set("Clear-Site-Data", `"cache"`)
	w.WriteHeader(http.StatusNoContent)
}
