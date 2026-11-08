package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed brand/*
var brandFiles embed.FS

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

func (s *Server) serveBrand(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/manifest.webmanifest" {
		text := s.catalogs[locale(r)]
		var icons struct {
			Icons []manifestIcon `json:"icons"`
		}
		data, err := brandFiles.ReadFile("brand/manifest-icons.json")
		if err != nil {
			http.Error(w, "", 500)
			return true
		}
		if err = json.Unmarshal(data, &icons); err != nil {
			http.Error(w, "", 500)
			return true
		}
		for i := range icons.Icons {
			icons.Icons[i].Src = brandURL(icons.Icons[i].Src)
		}
		manifest := struct {
			ID              string         `json:"id"`
			Name            string         `json:"name"`
			ShortName       string         `json:"short_name"`
			Description     string         `json:"description"`
			Lang            string         `json:"lang"`
			StartURL        string         `json:"start_url"`
			Scope           string         `json:"scope"`
			Display         string         `json:"display"`
			ThemeColor      string         `json:"theme_color"`
			BackgroundColor string         `json:"background_color"`
			Icons           []manifestIcon `json:"icons"`
		}{
			ID: "/", Name: text.AppName, ShortName: text.AppName,
			Description: text.AppDescription, Lang: locale(r),
			StartURL: "/", Scope: "/", Display: "standalone",
			ThemeColor: "#274c5e", BackgroundColor: "#f7f8f7", Icons: icons.Icons,
		}
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Vary", "Cookie, Accept-Language")
		if r.Method != http.MethodHead {
			_ = json.NewEncoder(w).Encode(manifest)
		}
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/ui/brand/") {
		return false
	}
	path := strings.TrimPrefix(r.URL.Path, "/ui/")
	if !fs.ValidPath(path) {
		http.NotFound(w, r)
		return true
	}
	data, err := brandFiles.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	switch {
	case strings.HasSuffix(path, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(path, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(path, ".json"):
		w.Header().Set("Content-Type", "application/json")
	case strings.HasSuffix(path, ".txt"):
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	default:
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(data)))
	http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(data))
	return true
}

// Content addressing invalidates browser/install icon caches when the shared
// distribution snapshot changes, without introducing runtime asset fetches.
func brandURL(name string) string {
	data, err := brandFiles.ReadFile("brand/" + name)
	if err != nil {
		return "/ui/brand/" + name
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("/ui/brand/%s?v=%x", name, digest[:8])
}
