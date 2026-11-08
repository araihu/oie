package web

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDocumentProxyStreamsSelectedOriginalWithoutInterpretingText(t *testing.T) {
	for _, kind := range []string{"text/plain", "application/pdf"} {
		for _, download := range []bool{false, true} {
			t.Run(kind+"/download="+strconv.FormatBool(download), func(t *testing.T) {
				body := "<script>window.untrusted=true</script>\nOlá\n"
				if kind == "application/pdf" {
					body = "%PDF-1.7\noriginal bytes"
				}
				var streams atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/engine" {
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.15.0", "eventCursor": testEngine + ":0"})
						return
					}
					streams.Add(1)
					if r.URL.Path != videoEnginePath || r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
						t.Error("noncanonical stream request or browser credentials forwarded", r.URL)
					}
					w.Header().Set("Content-Type", kind)
					w.Header().Set("Content-Length", strconv.Itoa(len(body)))
					w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "Arquivo português.txt"}))
					w.Header().Set("Set-Cookie", "provider=private")
					_, _ = io.WriteString(w, body)
				}))
				defer backend.Close()
				ui, err := New(backend.URL)
				if err != nil {
					t.Fatal(err)
				}
				target := videoProxyPath + "?engineId=" + testEngine
				if download {
					target += "&download=1"
				}
				r := httptest.NewRequest(http.MethodGet, target, nil)
				r.Header.Set("Cookie", "browser=private")
				r.Header.Set("Authorization", "Bearer private")
				w := httptest.NewRecorder()
				ui.ServeHTTP(w, r)
				if w.Code != 200 || w.Body.String() != body || streams.Load() != 1 || w.Header().Get("Content-Type") != kind {
					t.Fatalf("document stream changed: status=%d bytes=%q calls=%d", w.Code, w.Body.String(), streams.Load())
				}
				disposition, attributes, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
				want := "inline"
				if download {
					want = "attachment"
				}
				if err != nil || disposition != want || attributes["filename"] != "Arquivo português.txt" {
					t.Fatal("original download disposition lost", w.Header())
				}
				if w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Set-Cookie") != "" {
					t.Fatal("document response policy lost", w.Header())
				}
			})
		}
	}
}

func TestDocumentProxyEnforcesCompleteSizeForPartialReads(t *testing.T) {
	for _, tc := range []struct {
		name, kind, contentRange string
		length                   int64
		status                   int
	}{
		{"pdf range", "application/pdf", "bytes 2-5/1000", 4, 206},
		{"text range", "text/plain", "bytes 2-5/1000", 4, 206},
		{"oversize pdf range", "application/pdf", "bytes 2-5/104857601", 4, 502},
		{"oversize text range", "text/plain", "bytes 2-5/2097153", 4, 502},
		{"oversize text", "text/plain", "", 2097153, 502},
		{"oversize pdf", "application/pdf", "", 104857601, 502},
		{"active html", "text/html", "", 4, 502},
		{"active svg", "image/svg+xml", "", 4, 502},
		{"unnormalized json", "application/json", "", 4, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.kind)
				w.Header().Set("Content-Length", strconv.FormatInt(tc.length, 10))
				status := 200
				if tc.contentRange != "" {
					w.Header().Set("Content-Range", tc.contentRange)
					status = 206
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "1234")
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, videoProxyPath, nil)
			r.Header.Set("Range", "bytes=2-5")
			w := httptest.NewRecorder()
			ui.ServeHTTP(w, r)
			if w.Code != tc.status || tc.status == 502 && w.Body.Len() != 0 {
				t.Fatalf("unsafe document stream status=%d bytes=%d", w.Code, w.Body.Len())
			}
		})
	}
}

func TestDocumentDownloadRejectsAmbiguousParametersBeforeReadingEngine(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"download=0", "download=", "download=true", "download=1&download=1", "download=1&url=https://example.test/file.txt"} {
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest(http.MethodGet, videoProxyPath+"?"+query, nil))
		if w.Code != 400 || calls.Load() != 0 {
			t.Fatalf("invalid download selector contacted engine: %s status=%d calls=%d", query, w.Code, calls.Load())
		}
	}
}
