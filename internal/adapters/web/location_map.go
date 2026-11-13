package web

import (
	"bytes"
	"embed"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/araihu/oie/internal/adapters/cachehttp"
)

//go:embed location_map/*.mjs location_map/*.css location_map/*.txt location_map/*.geojson location_map.js
var locationMapFiles embed.FS

// WithLocationMapStyle opts into an external MapLibre style URL.
func WithLocationMapStyle(styleURL, attribution string) Option {
	return func(s *Server) {
		s.locationMapStyle = strings.TrimSpace(styleURL)
		s.locationMapAttribution = strings.TrimSpace(attribution)
	}
}

func validateLocationMapStyle(styleURL, attribution string) error {
	if styleURL == "" {
		return nil
	}
	u, err := url.Parse(styleURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
		strings.ContainsRune(styleURL, '\\') || strings.IndexFunc(styleURL, unicode.IsSpace) >= 0 {
		return errors.New("OIE_MAP_STYLE_URL must be an HTTPS URL without credentials or a fragment")
	}
	if attribution == "" || len(attribution) > 256 || strings.IndexFunc(attribution, unicode.IsControl) >= 0 {
		return errors.New("OIE_MAP_ATTRIBUTION must credit the external map in 256 bytes or fewer")
	}
	return nil
}

func serveLocationMapAsset(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/ui/location_map.js" && !strings.HasPrefix(r.URL.Path, "/ui/location_map/") {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	data, err := locationMapFiles.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	switch path.Ext(name) {
	case ".js", ".mjs":
		w.Header().Set("Content-Type", "text/javascript")
	case ".css":
		w.Header().Set("Content-Type", "text/css")
	case ".geojson":
		w.Header().Set("Content-Type", "application/geo+json")
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", cachehttp.Digest(data))
	cachehttp.Revalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
	})).ServeHTTP(w, r)
	return true
}
