package apphost

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestHostedProbeListenerIsSeparate(t *testing.T) {
	cfg := hostedConfig(t, t.TempDir(), true)
	cfg.ProbeListen = "127.0.0.1:0"
	s := testServer(t, cfg)
	if s.probe == nil || s.probe.Addr == "" {
		t.Fatal("probe listener was not opened")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	for _, path := range []string{"/livez", "/readyz", "/", "/api/v1/health", "/api/openapi.yaml", "/ui/diagnostics/log"} {
		response, err := client.Get("http://" + s.probe.Addr + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err := errors.Join(err, closeErr); err != nil {
			t.Fatal(err)
		}
		switch path {
		case "/livez", "/readyz":
			if response.StatusCode != http.StatusOK || string(body) != "ok" ||
				response.Header.Get("WWW-Authenticate") != "" || response.Header.Get("Cache-Control") != "no-store" {
				t.Errorf("unexpected probe response on %s: %d %q", path, response.StatusCode, body)
			}
			if w := hostedResponse(s, hostedRequest(http.MethodGet, path, "", nil)); w.Code != http.StatusNotFound {
				t.Errorf("public listener served a probe on %s: %d", path, w.Code)
			}
		default:
			if response.StatusCode != http.StatusNotFound {
				t.Errorf("probe listener exposed %s: %d %q", path, response.StatusCode, body)
			}
		}
	}
	if w := hostedResponse(s, hostedRequest(http.MethodGet, "/api/v1/health", "", nil)); w.Code != http.StatusUnauthorized {
		t.Fatalf("public engine health no longer requires authentication: %d", w.Code)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.probe.Addr); err == nil {
		_ = conn.Close()
		t.Fatal("probe listener stayed open after shutdown")
	}
}

func TestProbeLivenessDoesNotRequireAReadyEngine(t *testing.T) {
	handler := probeHandler(&engineHost{})
	for path, status := range map[string]int{"/livez": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != status {
			t.Errorf("%s: got %d, want %d", path, w.Code, status)
		}
	}
}

func TestProbeBindFailureLeavesEngineDataUntouched(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	dir := t.TempDir()
	_, err = Start(t.Context(), Config{Listen: "127.0.0.1:0", ProbeListen: listener.Addr().String(), DataDir: dir})
	if err == nil {
		t.Fatal("startup accepted an occupied probe address")
	}
	if _, err := os.Stat(filepath.Join(dir, "oie.sqlite")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed probe startup opened the engine database: %v", err)
	}
}

func TestProbeDrainDoesNotDelayApplicationShutdown(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer probe.Close()
	releaseProbe := sync.OnceFunc(func() { close(release) })
	defer releaseProbe()
	app := httptest.NewServer(http.NotFoundHandler())
	defer app.Close()
	appShutdown := make(chan struct{})
	app.Config.RegisterOnShutdown(func() { close(appShutdown) })
	requestDone := make(chan error, 1)
	go func() {
		response, err := probe.Client().Get(probe.URL)
		if err == nil {
			err = response.Body.Close()
		}
		requestDone <- err
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("probe request did not start")
	}
	drained := make(chan error, 1)
	go func() { drained <- shutdownHTTP(ctx, probe.Config, app.Config) }()
	select {
	case <-appShutdown:
	case <-ctx.Done():
		t.Fatal("application shutdown waited for the outstanding probe")
	}
	releaseProbe()
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
}
