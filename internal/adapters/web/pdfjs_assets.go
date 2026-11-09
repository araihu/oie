package web

import (
	"bytes"
	"embed"
	"github.com/araihu/oie/internal/adapters/cachehttp"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// The display layer, worker, fonts and image decoders are served locally.
// The PDF scripting sandbox is deliberately absent from this distribution.
//
//go:embed all:pdfjs
var pdfJSAssets embed.FS

func (s *Server) servePDFJS(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/ui/pdfjs/") {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	if !fs.ValidPath(name) {
		http.NotFound(w, r)
		return true
	}
	data, err := pdfJSAssets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	kind := "application/octet-stream"
	switch path.Ext(name) {
	case ".mjs", ".js":
		kind = "text/javascript"
	case ".css":
		kind = "text/css"
	case ".wasm":
		kind = "application/wasm"
	case ".json":
		kind = "application/json"
	case ".svg":
		kind = "image/svg+xml"
	case ".png":
		kind = "image/png"
	}
	if strings.Contains(path.Base(name), "LICENSE") {
		kind = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", cachehttp.Digest(data))
	cachehttp.Revalidate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
	})).ServeHTTP(w, r)
	return true
}
