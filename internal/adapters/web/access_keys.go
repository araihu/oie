package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/araihu/oie/internal/adapters/hostauth"
)

func (s *Server) hostedAccessKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.createHostedKey == nil || !hostauth.BrowserRequest(r.Context()) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	text := s.catalogs[locale(r)]
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		http.Error(w, text.AccessKeyInvalid, http.StatusBadRequest)
		return
	}
	if !validCSRF(r) {
		http.Error(w, text.CSRFError, http.StatusForbidden)
		return
	}
	secret, err := s.createHostedKey(r, r.PostForm.Get("name"))
	switch {
	case errors.Is(err, hostauth.ErrInvalidKey):
		http.Error(w, text.AccessKeyInvalid, http.StatusBadRequest)
	case err != nil:
		http.Error(w, text.AccessKeyFailed, http.StatusServiceUnavailable)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			Key string `json:"key"`
		}{Key: secret})
	}
}
