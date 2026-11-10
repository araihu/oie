//go:build !server

package desktop

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func nativeTestRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, "wails://localhost"+path, nil)
}
func TestNativeCookiesKeepOnlyDevicePreferencesAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := newNativeCookies(dir)
	if err != nil {
		t.Fatal(err)
	}
	firstCSRF := store.values["oie_csrf"]
	handler := store.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("oie_csrf")
		if cookie == nil || cookie.Value != firstCSRF {
			t.Error("native request has no session CSRF cookie")
		}
		http.SetCookie(w, &http.Cookie{Name: "oie_lang", Value: "pt-BR", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "oie_theme", Value: "dark", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "credential", Value: "secret", Path: "/"})
		w.Header().Set("Authorization", "secret")
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, nativeTestRequest("GET", "/"))
	if recorder.Code != 204 {
		t.Fatal(recorder.Code)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), firstCSRF) || strings.Contains(string(data), "secret") {
		t.Fatal("persisted session or credentials")
	}
	info, err := os.Stat(store.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("preference permissions: %v %v", info, err)
	}
	next, err := newNativeCookies(dir)
	if err != nil {
		t.Fatal(err)
	}
	if next.values["oie_lang"] != "pt-BR" || next.values["oie_theme"] != "dark" {
		t.Fatal(next.values)
	}
	if next.values["oie_csrf"] == firstCSRF {
		t.Fatal("CSRF session survived process restart")
	}
}

func TestNativeCookiesRejectForeignContextAndHostileCookies(t *testing.T) {
	store, err := newNativeCookies(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	handler := store.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if value, _ := r.Cookie("oie_csrf"); value == nil || value.Value == "attacker" {
			t.Error("accepted supplied native session")
		}
		if _, err := r.Cookie("credential"); err == nil {
			t.Error("forwarded arbitrary browser cookie")
		}
		w.WriteHeader(204)
	}))
	for _, scenario := range []struct{ url, key, value string }{
		{"https://attacker.invalid/", "", ""},
		{"wails://attacker.invalid/", "", ""},
		{"wails://localhost/", "Origin", "https://attacker.invalid"},
		{"wails://localhost/", "Referer", "https://attacker.invalid/"},
		{"wails://localhost/", "Sec-Fetch-Site", "cross-site"},
	} {
		r := httptest.NewRequest("POST", scenario.url, nil)
		if scenario.key != "" {
			r.Header.Set(scenario.key, scenario.value)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("foreign context accepted: %v", scenario)
		}
	}
	if called != 0 {
		t.Fatal("foreign request reached presentation")
	}
	r := nativeTestRequest("POST", "/")
	r.Header.Set("Origin", "wails://localhost")
	r.Header.Set("Cookie", "oie_csrf=attacker; credential=secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 204 || called != 1 {
		t.Fatal(w.Code, called)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Host = "localhost"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("Wails server request form rejected", w.Code)
	}
	r.Host = "attacker.invalid"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("foreign relative host accepted", w.Code)
	}
}

func TestNativeCookiesDoNotBlockMutationsBehindSSE(t *testing.T) {
	store, err := newNativeCookies(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	handler := store.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: ready\n\n")
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
			}
			<-release
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "oie_theme", Value: "dark", Path: "/"})
		w.WriteHeader(204)
	}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Scheme = "wails"
		r.URL.Host = "localhost"
		r.Host = "localhost"
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	defer close(release)
	client := &http.Client{Timeout: 2 * time.Second}
	events, err := client.Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	line, err := bufio.NewReader(events.Body).ReadString('\n')
	if err != nil || line != "data: ready\n" {
		t.Fatal(line, err)
	}
	changed, err := client.Post(server.URL+"/change", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = changed.Body.Close()
	if changed.StatusCode != 204 {
		t.Fatal(changed.StatusCode)
	}
}

func TestNativeCookiesPreserveRangeAndHeadResponses(t *testing.T) {
	store, err := newNativeCookies(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := store.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.mp4", time.Unix(1, 0), bytes.NewReader([]byte("0123456789")))
	}))
	for _, method := range []string{"GET", "HEAD"} {
		r := nativeTestRequest(method, "/clip.mp4")
		r.Header.Set("Range", "bytes=2-4")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 2-4/10" {
			t.Fatal(w.Code, w.Header())
		}
		expected := "234"
		if method == "HEAD" {
			expected = ""
		}
		if w.Body.String() != expected {
			t.Fatal(method, w.Body.String())
		}
	}
}

func TestNativePreferencesMergeConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	store, err := newNativeCookies(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, cookie := range []*http.Cookie{{Name: "oie_lang", Value: "pt-BR", Path: "/"}, {Name: "oie_theme", Value: "light", Path: "/"}} {
		wg.Go(func() {
			h := make(http.Header)
			h.Add("Set-Cookie", cookie.String())
			if err := store.capture(h); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	next, err := newNativeCookies(dir)
	if err != nil {
		t.Fatal(err)
	}
	if next.values["oie_lang"] != "pt-BR" || next.values["oie_theme"] != "light" {
		t.Fatal(next.values)
	}
}

func TestNativePreferencesRejectCorruptAndPublicFiles(t *testing.T) {
	for _, scenario := range []struct {
		data string
		mode os.FileMode
	}{
		{"{", 0600}, {`{"credential":"secret"}`, 0600}, {`{"oie_theme":"purple"}`, 0600}, {`{"oie_theme":"dark"}`, 0644},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "webview"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "webview", "preferences.json"), []byte(scenario.data), scenario.mode); err != nil {
			t.Fatal(err)
		}
		if _, err := newNativeCookies(dir); err == nil {
			t.Fatalf("accepted malformed or public preference file: %v", scenario)
		}
	}
}

func TestNativePreferenceWriteFailureDoesNotCommitSuccessfulResponse(t *testing.T) {
	store, err := newNativeCookies(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Replacing the intended file with a directory reliably rejects rename even
	// when tests run as root and Unix write permission bits would be bypassed.
	if err := os.Mkdir(store.path, 0700); err != nil {
		t.Fatal(err)
	}
	handler := store.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "oie_theme", Value: "dark", Path: "/"})
		_, _ = io.WriteString(w, "success")
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, nativeTestRequest("GET", "/"))
	if w.Code != 500 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if store.values["oie_theme"] != "" {
		t.Fatal("failed persistence changed active preference")
	}
}
