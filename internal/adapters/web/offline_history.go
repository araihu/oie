package web

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"

	"github.com/araihu/oie/internal/adapters/cachehttp"
)

//go:embed offline-worker.js
var offlineWorkerScript []byte

//go:embed offline-history.js
var offlineHistoryScript []byte

//go:embed offline-history.css
var offlineHistoryStylesheet []byte

//go:embed offline_history.templ
var offlineHistoryTemplate []byte

// Make the browser reinstall the worker when its precached assets change.
var offlineWorkerSource = func() []byte {
	hash := sha256.New()
	for _, asset := range [][]byte{offlineHistoryScript, offlineHistoryStylesheet, offlineHistoryTemplate} {
		hash.Write(asset)
	}
	for _, lang := range []string{"en", "pt-BR"} {
		catalog, _ := localeFiles.ReadFile("locales/" + lang + ".json")
		hash.Write(catalog)
	}
	return fmt.Appendf(bytes.Clone(offlineWorkerScript), "\n// Offline asset revision: %x\n", hash.Sum(nil))
}()

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
		body = offlineWorkerSource
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
