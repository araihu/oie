package web

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed media_encoder
var outgoingMediaAssets embed.FS

func serveOutgoingMedia(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/ui/media-encoder/")
	switch name {
	case "prepare.mjs", "choice.mjs", "NOTICES.txt":
		w.Header().Set("Cache-Control", "no-cache")
	case "v0.12.15-core0.12.10/ffmpeg.js", "v0.12.15-core0.12.10/814.ffmpeg.js", "v0.12.15-core0.12.10/ffmpeg-core.js", "v0.12.15-core0.12.10/ffmpeg-core.wasm":
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript")
	if name == "NOTICES.txt" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	if strings.HasSuffix(name, ".wasm") {
		w.Header().Set("Content-Type", "application/wasm")
	}
	http.ServeFileFS(w, r, outgoingMediaAssets, "media_encoder/"+name)
}

func (v view) uploadLimit() int64 {
	if c := v.MessagingCapabilities; c != nil && c.Discovery.UploadMaxBytes != nil {
		return *c.Discovery.UploadMaxBytes
	}
	return 0
}
