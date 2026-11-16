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
	remote := testServer(t, Config{Role: "local", DataOnly: true, DataDir: t.TempDir(), Listen: "127.0.0.1:0"})
	remoteID := engineIdentity(t, remote)
	dir := t.TempDir()
	var saved atomic.Value
	saved.Store("")
	var saveFailure atomic.Bool
	desktop, err := StartDesktop(t.Context(), dir, "", DesktopEngine{}, "", nil, func(selection DesktopEngine) error {
		if saveFailure.Load() {
			return errors.New("test save failure")
		}
		saved.Store(selection.URL)
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

func TestDesktopRestartsStoppedLocalEngine(t *testing.T) {
	desktop, err := StartDesktop(t.Context(), t.TempDir(), "", DesktopEngine{}, "", nil, func(DesktopEngine) error {
		t.Error("automatic restart rewrote the selected engine")
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
	defer app.Close()
	before := desktop.active.Load()
	workspaceIdentity := func() string {
		t.Helper()
		resp, err := app.Client().Get(app.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		match := regexp.MustCompile(`data-engine-id="([^"]+)"`).FindSubmatch(body)
		if err != nil || resp.StatusCode != http.StatusOK || len(match) != 2 {
			t.Fatalf("workspace did not render the local engine: %s %v", resp.Status, err)
		}
		return string(match[1])
	}
	id := workspaceIdentity()
	restart := func(previous *desktopTarget) *desktopTarget {
		t.Helper()
		previous.engine.beginClose()
		deadline := time.Now().Add(10 * time.Second)
		for desktop.active.Load() == previous && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		current := desktop.active.Load()
		if current == previous || current.engine == nil || current.token != previous.token {
			t.Fatal("desktop did not restart the selected local engine")
		}
		if got := workspaceIdentity(); got != id {
			t.Fatalf("recovered engine identity = %s, want %s", got, id)
		}
		if current.engine.ctx.Err() != nil {
			t.Fatal("recovered engine was stopped with old target")
		}
		return current
	}
	after := restart(before)
	failedAgain := time.Now()
	restart(after)
	if elapsed := time.Since(failedAgain); elapsed < time.Second {
		t.Fatalf("restarted a short-lived replacement without backoff after %s", elapsed)
	}
}

func TestDesktopRemoteStartupDoesNotOpenLocalData(t *testing.T) {
	dir := t.TempDir()
	remote := testServer(t, Config{Role: "local", DataOnly: true, DataDir: t.TempDir(), Listen: "127.0.0.1:0"})
	desktop, err := StartDesktop(t.Context(), dir, "", DesktopEngine{URL: remote.URL()}, "", nil, func(DesktopEngine) error { return nil })
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

func TestDesktopKeyStaysAtSelectedOrigin(t *testing.T) {
	const token = "example-desktop-key"
	for _, tc := range []struct {
		selected, target string
		sameOrigin       bool
	}{
		{"https://oie.example.test", "https://OIE.example.test:443", true},
		{"https://oie.example.test", "https://oie.example.test:8443", false},
		{"https://oie.example.test", "http://oie.example.test", false},
		{"https://oie.example.test", "https://remote.example.test", false},
		{"https://σ.example.test", "https://σ.example.test:443", true},
		{"https://σ.example.test", "https://ς.example.test", false},
		{"https://[fe80::1%25eth0]", "https://[fe80::1%25ETH0]", false},
	} {
		t.Run(tc.selected+" to "+tc.target, func(t *testing.T) {
			first, second := desktopKeyOrigin(tc.selected), desktopKeyOrigin(tc.target)
			if first == "" || (first == second) != tc.sameOrigin {
				t.Fatal("incorrect origin comparison for reusing the saved key")
			}
			edit, err := desktopKeyEditor(DesktopEngine{URL: tc.selected, APIKey: token})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodGet, tc.target+"/api/v1/engine", nil)
			if err := edit(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			header := r.Header.Get("Authorization")
			if tc.sameOrigin && header != "Bearer "+token || !tc.sameOrigin && header != "" {
				t.Fatal("incorrect authorization for the outbound request")
			}
		})
	}
	edit, err := desktopKeyEditor(DesktopEngine{URL: "https://oie.example.test", APIKey: token})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://oie.example.test/api/v1/engine", nil)
	r.Host = "other.example.test"
	if err := edit(t.Context(), r); err != nil || r.Header.Get("Authorization") != "" {
		t.Fatal("sent a key with another HTTP authority")
	}
	if _, err := desktopKeyEditor(DesktopEngine{URL: "http://oie.example.test", APIKey: token}); err == nil {
		t.Fatal("accepted an access key over HTTP")
	}
}
