package apphost

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopEngineSelection(t *testing.T) {
	remote := testServer(t, Config{Role: "hosted", DataOnly: true, DataDir: t.TempDir(), Listen: "127.0.0.1:0"})
	remoteID := engineIdentity(t, remote)
	dir := t.TempDir()
	var saved atomic.Value
	saved.Store("")
	var saveFailure atomic.Bool
	desktop, err := StartDesktop(t.Context(), dir, "", "", nil, func(address string) error {
		if saveFailure.Load() {
			return errors.New("test save failure")
		}
		saved.Store(address)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := desktop.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	app := httptest.NewServer(desktop)
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	workspace := func(mode string) (id, selection, csrf string) {
		t.Helper()
		resp, err := client.Get(app.URL + "/?lang=pt-BR")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("workspace: %s %v", resp.Status, err)
		}
		match := func(pattern string) string {
			t.Helper()
			value := regexp.MustCompile(pattern).FindSubmatch(body)
			if len(value) != 2 {
				t.Fatalf("missing %s", pattern)
			}
			return string(value[1])
		}
		match(`(id="engine-mode-` + mode + `"[^>]+checked)`)
		return match(`data-engine-id="([^"]+)"`), match(`name="selection" value="([^"]+)"`), match(`name="csrf" value="([^"]+)"`)
	}
	submit := func(selection, csrf, mode, address string, expected int) {
		t.Helper()
		form := url.Values{"selection": {selection}, "csrf": {csrf}, "mode": {mode}, "url": {address}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, app.URL+"/ui/engine-mode", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != expected {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("select %s: got %s, want %d (%s)", mode, res.Status, expected, body)
		}
	}

	localID, selection, csrf := workspace("local")
	initial := desktop.active.Load()
	if localID == remoteID || initial.engine == nil {
		t.Fatalf("expected embedded engine, got %s", localID)
	}
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, remote.URL(), http.StatusFound)
	}))
	t.Cleanup(unavailable.Close)
	submit(selection, "", "remote", remote.URL(), 403)
	submit(selection, csrf, "remote", "file:///tmp/oie", 400)
	submit(selection, csrf, "remote", unavailable.URL, 503)
	saveFailure.Store(true)
	submit(selection, csrf, "remote", remote.URL(), 500)
	saveFailure.Store(false)
	if desktop.active.Load() != initial || saved.Load() != "" {
		t.Fatal("failed selection changed the engine or preference")
	}
	if id, _, _ := workspace("local"); id != localID {
		t.Fatalf("failed selection changed local engine to %q", id)
	}

	submit(selection, csrf, "remote", remote.URL(), 204)
	if id, _, _ := workspace("remote"); id != remoteID || saved.Load() != remote.URL() {
		t.Fatalf("selected engine = %q, preference = %q", id, saved.Load())
	}
	select {
	case <-initial.engine.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("local engine did not stop in remote mode")
	}
	submit(selection, csrf, "local", "", 409)

	_, selection, csrf = workspace("remote")
	submit(selection, csrf, "local", "", 204)
	if id, _, _ := workspace("local"); id != localID || saved.Load() != "" {
		t.Fatalf("restored engine = %q, preference = %q", id, saved.Load())
	}
	if id := engineIdentity(t, remote); id != remoteID {
		t.Fatalf("remote engine changed after switching back: %s", id)
	}
}

func TestDesktopRemoteStartupDoesNotOpenLocalData(t *testing.T) {
	dir := t.TempDir()
	remote := testServer(t, Config{Role: "hosted", DataOnly: true, DataDir: t.TempDir(), Listen: "127.0.0.1:0"})
	desktop, err := StartDesktop(t.Context(), dir, "", remote.URL(), nil, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := desktop.Close(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	if _, err := os.Stat(filepath.Join(dir, "oie.sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remote startup opened local data: %v", err)
	}
}
