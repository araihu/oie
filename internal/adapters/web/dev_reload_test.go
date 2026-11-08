package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevReloadEndpointsRequireExplicitEnablement(t *testing.T) {
	for _, path := range []string{"/ui/dev/build", "/ui/dev-reload.js"} {
		s := &Server{}
		w := httptest.NewRecorder()
		if !s.serveDevReload(w, httptest.NewRequest(http.MethodGet, path, nil)) || w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("disabled dev endpoint exposed", path, w.Code)
		}
	}
	s := &Server{}
	WithDevReload()(s)
	other := &Server{}
	WithDevReload()(other)
	if s.devBuildID == "" || s.devBuildID == other.devBuildID {
		t.Fatal("markers must identify server startup")
	}
	for range 2 {
		w := httptest.NewRecorder()
		s.serveDevReload(w, httptest.NewRequest(http.MethodGet, "/ui/dev/build", nil))
		var marker struct {
			BuildID string `json:"buildId"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &marker); err != nil || marker.BuildID != s.devBuildID || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("unstable or cacheable build marker", w, err)
		}
	}
	w := httptest.NewRecorder()
	s.serveDevReload(w, httptest.NewRequest(http.MethodGet, "/ui/dev-reload.js", nil))
	if !strings.Contains(w.Body.String(), "document.currentScript") || w.Header().Get("Content-Type") != "text/javascript" {
		t.Fatal("reload script missing", w)
	}
	w = httptest.NewRecorder()
	s.serveDevReload(w, httptest.NewRequest(http.MethodPost, "/ui/dev/build", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal("mutation accepted", w)
	}
	w = httptest.NewRecorder()
	s.serveDevReload(w, httptest.NewRequest(http.MethodHead, "/ui/dev/build", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD body", w)
	}
}
func TestDevReloadInitialHTMLBindsStartupToken(t *testing.T) {
	for _, token := range []string{"", "development-token"} {
		var output bytes.Buffer
		if err := page(view{DevBuildID: token}).Render(t.Context(), &output); err != nil {
			t.Fatal(err)
		}
		body := output.String()
		if token == "" {
			if strings.Contains(body, "/ui/dev-reload.js") {
				t.Fatal("production includes dev poller")
			}
		} else if !strings.Contains(body, `data-build-id="`+token+`"`) || !strings.Contains(body, `src="/ui/dev-reload.js"`) {
			t.Fatal("HTML lacks initial process token")
		}
	}
}
