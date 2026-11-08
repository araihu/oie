package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPDFJSDistributionMatchesPinnedManifestAndIncludesNotices(t *testing.T) {
	data, err := pdfJSAssets.ReadFile("pdfjs/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version, Integrity string
		Files              map[string]string
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "6.5.438" || !strings.HasPrefix(manifest.Integrity, "sha512-") {
		t.Fatal("unexpected PDF.js release", manifest.Version)
	}
	count := 0
	err = fs.WalkDir(pdfJSAssets, "pdfjs", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || name == "pdfjs/manifest.json" {
			return nil
		}
		body, err := pdfJSAssets.ReadFile(name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(body)
		if manifest.Files[strings.TrimPrefix(name, "pdfjs/")] != hex.EncodeToString(digest[:]) {
			t.Errorf("unlisted or changed PDF.js asset %s", name)
		}
		if strings.Contains(strings.ToLower(name), "quickjs") || strings.Contains(name, "pdf.sandbox") || strings.Contains(name, "Liberation") {
			t.Errorf("unselected scripting or font asset %s", name)
		}
		count++
		return nil
	})
	if err != nil || count != len(manifest.Files) {
		t.Fatalf("distribution inventory mismatch count=%d expected=%d err=%v", count, len(manifest.Files), err)
	}
	for _, required := range []string{"LICENSE", "cmaps/LICENSE", "standard_fonts/LICENSE_FOXIT", "wasm/LICENSE_OPENJPEG", "wasm/LICENSE_JBIG2", "wasm/LICENSE_QCMS", "iccs/LICENSE", "build/pdf.mjs", "build/pdf.worker.mjs", "web/pdf_viewer.css"} {
		if _, exists := manifest.Files[required]; !exists {
			t.Error("missing display asset or notice", required)
		}
	}
}

func TestPDFJSAssetsAreLocalConditionalResourcesWithNoDirectoryBrowsing(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct{ path, kind string }{{"build/pdf.mjs", "text/javascript"}, {"build/pdf.worker.mjs", "text/javascript"}, {"wasm/openjpeg.wasm", "application/wasm"}, {"web/pdf_viewer.css", "text/css"}, {"LICENSE", "text/plain; charset=utf-8"}} {
		w := httptest.NewRecorder()
		if !s.servePDFJS(w, httptest.NewRequest(http.MethodGet, "/ui/pdfjs/"+tc.path, nil)) || w.Code != 200 || w.Header().Get("Content-Type") != tc.kind || w.Body.Len() == 0 || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("missing or invalid PDF display resource %s: %d %v", tc.path, w.Code, w.Header())
		}
		r := httptest.NewRequest(http.MethodGet, "/ui/pdfjs/"+tc.path, nil)
		r.Header.Set("If-None-Match", w.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		s.servePDFJS(cached, r)
		if cached.Code != 304 || cached.Body.Len() != 0 {
			t.Fatal("unchanged static dependency was retransmitted", tc.path, cached.Code)
		}
	}
	for _, name := range []string{"", "../server.go", "build/", "build/pdf.sandbox.mjs", "standard_fonts/LiberationSans-Regular.ttf"} {
		w := httptest.NewRecorder()
		s.servePDFJS(w, httptest.NewRequest(http.MethodGet, "/ui/pdfjs/"+name, nil))
		if w.Code != 404 {
			t.Fatal("exposed a directory or unselected resource", name, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.servePDFJS(w, httptest.NewRequest(http.MethodPost, "/ui/pdfjs/build/pdf.mjs", nil))
	if w.Code != 405 {
		t.Fatal("static display resource accepted a mutation method", w.Code)
	}
}
