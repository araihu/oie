package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed libmedia/5978d2ba
var videoDecoderAssets embed.FS

func serveVideoDecoderAsset(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/ui/libmedia/5978d2ba/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	name := strings.TrimPrefix(r.URL.Path, "/ui/")
	info, err := fs.Stat(videoDecoderAssets, name)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFileFS(w, r, videoDecoderAssets, name)
	return true
}
