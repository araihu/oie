package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestBFFUsesHTTPAndDeviceScopedPresentation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_, _ = w.Write([]byte(`{"id":"019a5df0-0000-7000-8000-000000000001","role":"hosted","apiVersion":"0.1.0","eventCursor":"019a5df0-0000-7000-8000-000000000001:0"}`))
		case "/api/v1/conversations":
			http.NotFound(w, r)
		case "/api/v1/accounts":
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			t.Errorf("unexpected BFF API request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ url, accept, wantLang, wantText string }{
		{"/", "pt-BR,pt;q=0.9", "pt-BR", "Nenhuma conta ainda"},
		{"/?lang=en&theme=dark", "pt-BR", "en", "No accounts yet"},
		{"/", "ja", "en", "No accounts yet"},
	} {
		r := httptest.NewRequest("GET", tc.url, nil)
		r.Header.Set("Accept-Language", tc.accept)
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `lang="`+tc.wantLang+`"`) || !strings.Contains(w.Body.String(), tc.wantText) {
			t.Fatalf("BFF locale failure: status=%d body=%s", w.Code, w.Body.String())
		}
		if tc.url != "/" && (!strings.Contains(w.Body.String(), `class="dark"`) || len(w.Result().Cookies()) != 2) {
			t.Fatal("device theme/language overrides not retained")
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "<!DOCTYPE") || !strings.Contains(w.Body.String(), `id="workspace"`) {
		t.Fatal("HTMX route returned an entire document or lost fragment identity")
	}
	backend.Close()
	w = httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Engine unavailable") || !strings.Contains(w.Body.String(), "<main") {
		t.Fatal("transport failure did not render recovery inside shell")
	}
	w = httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest("GET", "/missing", nil))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "Page not found") || !strings.Contains(w.Body.String(), "<main") {
		t.Fatal("unknown route did not retain the shell")
	}
}

func TestCatalogsHaveAllTypedExpressions(t *testing.T) {
	catalogs, err := loadCatalogs()
	if err != nil {
		t.Fatal(err)
	}
	for locale, text := range catalogs {
		value := reflect.ValueOf(text)
		for field := range value.NumField() {
			if strings.TrimSpace(value.Field(field).String()) == "" {
				t.Errorf("%s missing %s", locale, value.Type().Field(field).Name)
			}
		}
	}
	var en, pt map[string]string
	enData, _ := localeFiles.ReadFile("locales/en.json")
	ptData, _ := localeFiles.ReadFile("locales/pt-BR.json")
	if err := json.Unmarshal(enData, &en); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ptData, &pt); err != nil {
		t.Fatal(err)
	}
	if len(en) != len(pt) {
		t.Fatal("catalog key drift")
	}
	for key := range en {
		if _, ok := pt[key]; !ok {
			t.Errorf("pt-BR missing %s", key)
		}
	}
}
