package web

import (
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"net/http"
)

// Option configures an explicitly enabled BFF facility.
type Option func(*Server)

// WithDevReload exposes a process marker for the local frontend development
// supervisor. Normal servers do not expose the marker or its browser script.
func WithDevReload() Option {
	return func(s *Server) { s.devBuildID = rand.Text() }
}

//go:embed dev-reload.js
var devReloadScript []byte

func (s *Server) serveDevReload(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/ui/dev/build" && r.URL.Path != "/ui/dev-reload.js" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.devBuildID == "" {
		http.NotFound(w, r)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	if r.URL.Path == "/ui/dev-reload.js" {
		w.Header().Set("Content-Type", "text/javascript")
		if r.Method == http.MethodGet {
			_, _ = w.Write(devReloadScript)
		}
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(struct {
			BuildID string `json:"buildId"`
		}{s.devBuildID})
	}
	return true
}
