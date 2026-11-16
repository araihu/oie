package web

import (
	_ "embed"
	"net/http"

	"github.com/araihu/oie/internal/adapters/cachehttp"
)

//go:embed offline-worker.js
var offlineWorkerScript []byte

//go:embed offline-history.js
var offlineHistoryScript []byte

//go:embed offline-history.css
var offlineHistoryStylesheet []byte

func (s *Server) offlineHistoryEnabled() bool {
	return s.createHostedKey == nil && s.engineMode == nil
}

func (s *Server) serveOfflineHistory(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/offline-worker.js", "/ui/offline-history", "/ui/offline-history.js", "/ui/offline-history.css":
	default:
		return false
	}
	if !s.offlineHistoryEnabled() {
		http.NotFound(w, r)
		return true
	}
	var body []byte
	switch r.URL.Path {
	case "/offline-worker.js":
		body = offlineWorkerScript
	case "/ui/offline-history.js":
		body = offlineHistoryScript
	case "/ui/offline-history.css":
		body = offlineHistoryStylesheet
	case "/ui/offline-history":
		lang := locale(r)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = offlineHistoryPage(lang, s.catalogs[lang]).Render(r.Context(), w)
		return true
	}

	contentType := "text/javascript; charset=utf-8"
	if r.URL.Path == "/ui/offline-history.css" {
		contentType = "text/css; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", cachehttp.Digest(body))
	cachehttp.Revalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})).ServeHTTP(w, r)
	return true
}
