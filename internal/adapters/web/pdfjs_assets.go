package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
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
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(data)))
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
	return true
}
