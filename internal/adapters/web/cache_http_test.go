package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestScopedBrowserContentRevalidatesAfterEngineCheck(t *testing.T) {
	engine := testEngine
	reads := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/engine" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": engine, "role": "hosted", "apiVersion": "0.19.0", "eventCursor": engine + ":0"})
			return
		}
		reads++
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "5")
		w.Header().Set("ETag", `"source-v1"`)
		_, _ = w.Write([]byte("hello"))
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, tag string
		code       int
		policy     string
	}{{"?engineId=" + testEngine, "", 200, "private, no-cache"}, {"?engineId=" + testEngine, `W/"source-v1"`, 304, "private, no-cache"}, {"?engineId=" + testEngine + "&download=1", `"source-v1"`, 200, "no-store"}, {"", `"source-v1"`, 200, "no-store"}} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("GET", videoProxyPath+tc.query, nil)
		request.Header.Set("If-None-Match", tc.tag)
		ui.ServeHTTP(response, request)
		if response.Code != tc.code || response.Header().Get("Cache-Control") != tc.policy {
			t.Fatalf("%+v: %d %v", tc, response.Code, response.Header())
		}
		if tc.code == 304 && response.Body.Len() != 0 {
			t.Fatal("304 body")
		}
	}
	before := reads
	engine = testChat
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", videoProxyPath+"?engineId="+testEngine, nil)
	request.Header.Set("If-None-Match", `"source-v1"`)
	ui.ServeHTTP(response, request)
	if response.Code != 409 || response.Header().Get("Cache-Control") != "no-store" || reads != before {
		t.Fatal("conditional bypassed engine ownership", response)
	}
}
func TestBrowserAssetsShareConditionalDecorator(t *testing.T) {
	ui, err := New("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/ui/emoji/en.json", "/ui/emoji/pt-BR.json", "/ui/emoji-picker.js", "/ui/cache.js", "/ui/cache/lru-cache.min.mjs", "/ui/documents.js", "/ui/app.css", "/ui/pdfjs/build/pdf.mjs"} {
		first := httptest.NewRecorder()
		ui.ServeHTTP(first, httptest.NewRequest("GET", path, nil))
		tag := first.Header().Get("ETag")
		if first.Code != 200 || tag == "" || first.Header().Get("Cache-Control") != "no-cache" {
			t.Fatal(path, first.Header())
		}
		second := httptest.NewRecorder()
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("If-None-Match", "W/"+tag)
		ui.ServeHTTP(second, request)
		if second.Code != 304 || second.Body.Len() != 0 {
			t.Fatal(path, second)
		}
		if path == "/ui/cache/lru-cache.min.mjs" && strings.Contains(first.Body.String(), "from 'node:") {
			t.Fatal("Node-only bundle")
		}
	}
}

func TestCacheLibraryMatchesPinnedManifest(t *testing.T) {
	body, err := os.ReadFile("cache_vendor/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(cacheLibrary)
	if manifest.Version != "11.2.6" || manifest.SHA256 != hex.EncodeToString(digest[:]) || !strings.Contains(string(cacheLibraryLicense), "Blue Oak Model License") {
		t.Fatal("cache runtime integrity/license drift")
	}
}
