package apphost

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/hostauth"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/diagnostics"
	"github.com/google/uuid"
)

func TestDiagnosticsProtectRealEngineRequests(t *testing.T) {
	for _, mode := range []diagnostics.Mode{diagnostics.Local, diagnostics.Hosted} {
		t.Run(string(mode), func(t *testing.T) {
			for _, level := range []string{"info", "debug"} {
				t.Run(level, func(t *testing.T) { checkEngineLogPrivacy(t, mode, level == "debug") })
			}
		})
	}
}

func checkEngineLogPrivacy(t *testing.T, mode diagnostics.Mode, debug bool) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private-profile-sentinel")
	logDir := filepath.Join(dir, "diagnostics")
	logOutput, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logOutput.Close() })
	logger := diagnostics.New(logDir, logOutput, mode, debug)
	const key = "private-diagnostics-key-sentinel"
	cfg := Config{Role: string(mode), Listen: "127.0.0.1:0", DataDir: dir}
	var bearer string
	if mode == diagnostics.Hosted {
		cfg = hostedConfig(t, dir, false)
		_, bearer, err = hostauth.NewStore(dir).CreateKey(t.Context(), "diagnostics fixture", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg.Diagnostics, cfg.DiagnosticsKey = logger, key
	server := testServer(t, cfg)
	base := server.URL()
	if bearer != "" {
		front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Host = "oie.example.test"
			r.Header.Set("X-Forwarded-Proto", "https")
			server.server.Handler.ServeHTTP(w, r)
		}))
		t.Cleanup(front.Close)
		base = front.URL
	}

	// Supply real account and chat data to the same API served in both modes.
	const jid = "private-jid@example.invalid"
	const peer = "private-peer@example.invalid"
	const label = "private-name-sentinel"
	const password = "private-password-sentinel"
	const message = "message-content-sentinel https://private.example/secret.pdf?token=private-query-sentinel"
	client, err := engineapi.NewClientWithResponses(base, engineapi.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	newKey := func() string { return uuid.Must(uuid.NewV7()).String() }
	account, err := client.RegisterAccountWithResponse(ctx, &engineapi.RegisterAccountParams{IdempotencyKey: newKey()}, engineapi.RegisterAccountJSONRequestBody{
		Protocol: "xmpp", Address: jid, Label: label,
	})
	if err != nil || account.JSON201 == nil {
		t.Fatal("register fixture account:", err)
	}
	conversation, err := client.OpenConversationWithResponse(ctx, &engineapi.OpenConversationParams{IdempotencyKey: newKey()}, engineapi.OpenConversationJSONRequestBody{
		AccountId: account.JSON201.Id, Address: peer,
	})
	if err != nil || conversation.JSON201 == nil {
		t.Fatal("open fixture conversation:", err)
	}
	accepted, err := client.SendMessageWithResponse(ctx, conversation.JSON201.Id, &engineapi.SendMessageParams{IdempotencyKey: newKey()}, engineapi.SendMessage{
		OriginalText: message,
	})
	if err != nil || accepted.JSON202 == nil || accepted.JSON202.OriginalText != message {
		t.Fatal("fixture message was not accepted:", err)
	}
	// A real network failure commonly exposes the endpoint in its error string.
	socket, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := socket.Addr().String()
	_ = socket.Close()
	result, err := client.ConfigureAccountConnectionWithResponse(ctx, account.JSON201.Id, engineapi.ConfigureConnection{
		Endpoint: &endpoint, Password: new(password),
	})
	if err != nil || result.JSON202 == nil {
		t.Fatal("configure fixture account:", err)
	}
	var snapshot []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, _, err = logger.Snapshot(24)
		if err == nil && bytes.Contains(snapshot, []byte(diagnostics.XMPPConnectFailed)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !bytes.Contains(snapshot, []byte(diagnostics.XMPPConnectFailed)) {
		t.Fatal("connection failure did not leave diagnostic evidence", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+web.DiagnosticsPath+"?hours=24", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Oie-Diagnostics", "1")
	req.Header.Set("X-Oie-Diagnostics-Key", key)
	req.Header.Set("Authorization", "Bearer private-auth-header-sentinel")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Cookie", "oie_session=private-cookie-sentinel")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 || !diagnostics.ValidSnapshot(string(body)) || !bytes.Contains(body, []byte(diagnostics.XMPPConnectFailed)) {
		t.Fatalf("expected private-safe diagnostics export, status %s: %v", resp.Status, err)
	}
	var sinks = [][]byte{body}
	for _, path := range []string{filepath.Join(logDir, "oie-diagnostics.jsonl"), logOutput.Name()} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sinks = append(sinks, raw)
	}
	for _, sink := range sinks {
		for _, secret := range []string{
			key, jid, peer, label, password, "message-content-sentinel", "private.example", "secret.pdf", "private-query-sentinel",
			"private-profile-sentinel", endpoint, account.JSON201.Id, conversation.JSON201.Id, accepted.JSON202.Id,
			"private-auth-header-sentinel", "private-cookie-sentinel",
		} {
			if bytes.Contains(sink, []byte(secret)) || (bearer != "" && bytes.Contains(sink, []byte(bearer))) {
				t.Fatal("private fixture data reached a diagnostic sink")
			}
		}
	}
}

func TestDesktopDiagnosticsSurvivesEmbeddedStartupFailure(t *testing.T) {
	dir := t.TempDir()
	testServer(t, Config{Role: "local", Listen: "127.0.0.1:0", DataDir: dir})
	logger := diagnostics.New(filepath.Join(t.TempDir(), "diagnostics"), nil, diagnostics.Desktop, false)
	desktop, err := StartDesktop(t.Context(), dir, "", "", "", logger, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = desktop.Close(t.Context()) })
	workspace := httptest.NewRecorder()
	desktop.ServeHTTP(workspace, httptest.NewRequest(http.MethodGet, "/?lang=pt-BR", nil))
	if workspace.Code != http.StatusServiceUnavailable || !strings.Contains(workspace.Body.String(), "data-diagnostics") {
		t.Fatal("desktop startup failure hid Diagnostics")
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/diagnostics/log", nil)
	req.Header.Set("X-Oie-Diagnostics", "1")
	response := httptest.NewRecorder()
	desktop.ServeHTTP(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), string(diagnostics.StorageFailed)) || strings.Contains(response.Body.String(), dir) {
		t.Fatal("desktop could not inspect the sanitized startup failure")
	}
}

func TestDesktopCanReadLegacyDataOnlyDiagnostics(t *testing.T) {
	const key = "private-hosted-access-key"
	dir := t.TempDir()
	logger := diagnostics.New(filepath.Join(dir, "diagnostics"), nil, diagnostics.Hosted, false)
	engine := testServer(t, Config{
		Role: "local", DataOnly: true, Listen: "127.0.0.1:0", DataDir: dir, Diagnostics: logger, DiagnosticsKey: key,
	})
	local := diagnostics.New(filepath.Join(t.TempDir(), "diagnostics"), nil, diagnostics.Desktop, false)
	desktop, err := StartDesktop(t.Context(), t.TempDir(), "", engine.URL(), "", local, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = desktop.Close(t.Context()) })
	request := httptest.NewRequest(http.MethodGet, "/ui/diagnostics/log?source=server", nil)
	request.Header.Set("X-Oie-Diagnostics", "1")
	denied := httptest.NewRecorder()
	desktop.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden || denied.Body.Len() != 0 {
		t.Fatal("native request bypassed the hosted access check")
	}
	request.Header.Set("X-Oie-Diagnostics-Key", key)
	allowed := httptest.NewRecorder()
	desktop.ServeHTTP(allowed, request)
	if allowed.Code != 200 || !diagnostics.ValidSnapshot(allowed.Body.String()) || !strings.Contains(allowed.Body.String(), string(diagnostics.Ready)) {
		t.Fatalf("hosted data-only log unavailable in desktop: %d", allowed.Code)
	}
}

func TestHostedDiagnosticsRequiresOwnerCredentials(t *testing.T) {
	for _, dataOnly := range []bool{false, true} {
		dir := t.TempDir()
		cfg := hostedConfig(t, dir, dataOnly)
		cfg.Diagnostics = diagnostics.New(filepath.Join(dir, "diagnostics"), nil, diagnostics.Hosted, false)
		cfg.DiagnosticsKey = "legacy-diagnostics-test-key"
		s := testServer(t, cfg)

		req := hostedRequest(http.MethodGet, web.DiagnosticsPath, "", nil)
		req.Header.Set("X-Oie-Diagnostics", "1")
		req.Header.Set("X-Oie-Diagnostics-Key", cfg.DiagnosticsKey)
		if w := hostedResponse(s, req); w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), string(diagnostics.Ready)) {
			t.Fatalf("legacy diagnostics key exposed hosted logs: %d %s", w.Code, w.Body)
		}

		_, key, err := hostauth.NewStore(dir).CreateKey(t.Context(), "fixture", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req = hostedRequest(http.MethodGet, web.DiagnosticsPath, key, nil)
		req.Header.Set("X-Oie-Diagnostics", "1")
		if w := hostedResponse(s, req); w.Code != http.StatusOK || !diagnostics.ValidSnapshot(w.Body.String()) ||
			!strings.Contains(w.Body.String(), string(diagnostics.Ready)) || strings.Contains(w.Body.String(), key) {
			t.Fatalf("authenticated read of hosted diagnostics failed: %d %s", w.Code, w.Body)
		}
	}
}
