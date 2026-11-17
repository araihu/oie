package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPDFJSDistributionIncludesNoticesAndExcludesScripting(t *testing.T) {
	err := fs.WalkDir(pdfJSAssets, "pdfjs", func(name string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(name), "quickjs") || strings.Contains(name, "pdf.sandbox") {
			t.Errorf("unselected scripting asset %s", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"LICENSE", "cmaps/LICENSE", "standard_fonts/LICENSE_FOXIT", "standard_fonts/LICENSE_LIBERATION",
		"wasm/LICENSE_OPENJPEG", "wasm/LICENSE_JBIG2", "wasm/LICENSE_QCMS", "iccs/LICENSE",
		"build/pdf.mjs", "build/pdf.worker.mjs", "web/pdf_viewer.css",
	} {
		if _, err := pdfJSAssets.ReadFile("pdfjs/" + required); err != nil {
			t.Errorf("missing display asset or notice %s: %v", required, err)
		}
	}
}

func TestPDFJSAssetsAreLocalConditionalResourcesWithNoDirectoryBrowsing(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct{ path, kind string }{
		{"build/pdf.mjs", "text/javascript"},
		{"build/pdf.worker.mjs", "text/javascript"},
		{"wasm/openjpeg.wasm", "application/wasm"},
		{"web/pdf_viewer.css", "text/css"},
		{"LICENSE", "text/plain; charset=utf-8"},
		{"standard_fonts/LICENSE_LIBERATION", "text/plain; charset=utf-8"},
		{"standard_fonts/LiberationSans-Regular.ttf", "font/ttf"},
		{"standard_fonts/LiberationSans-Bold.ttf", "font/ttf"},
		{"standard_fonts/LiberationSans-Italic.ttf", "font/ttf"},
		{"standard_fonts/LiberationSans-BoldItalic.ttf", "font/ttf"},
	} {
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
	for _, name := range []string{"", "../server.go", "build/", "build/pdf.sandbox.mjs", "build/unlocked.mjs", "standard_fonts/"} {
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
