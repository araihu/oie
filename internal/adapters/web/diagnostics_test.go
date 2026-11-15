package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/diagnostics"
)

func TestHostedDiagnosticsRequiresKeyAndReturnsUncachedSafeSnapshot(t *testing.T) {
	const key = "private-key-test-sentinel"
	logger := diagnostics.New(filepath.Join(t.TempDir(), "logs"), nil, diagnostics.Hosted, false)
	logger.Record(diagnostics.Starting, 0, 0)
	s, err := New("http://127.0.0.1:1", WithDiagnostics(logger, key, DiagnosticsKeyRequired))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, marker, key, site string
		status                  int
	}{
		{"hosted loopback does not grant access", "1", "", "", 403},
		{"wrong key", "1", "other-key", "", 403},
		{"marker required", "", key, "", 403},
		{"cross origin", "1", key, "cross-site", 403},
		{"same origin", "1", key, "same-origin", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+DiagnosticsPath, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set(diagnosticsMarker, tc.marker)
			req.Header.Set(diagnosticsKey, tc.key)
			req.Header.Set("Sec-Fetch-Site", tc.site)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != tc.status || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("got status %d, cache %q", rec.Code, rec.Header().Get("Cache-Control"))
			}
			if tc.status == 200 {
				onDisk, _, err := logger.Snapshot(24)
				if err != nil || !bytes.Equal(rec.Body.Bytes(), onDisk) || !diagnostics.ValidSnapshot(rec.Body.String()) {
					t.Fatalf("served unexpected snapshot: %v", err)
				}
			} else if rec.Body.Len() != 0 {
				t.Fatal("denial returned data")
			}
			if strings.Contains(rec.Body.String(), key) {
				t.Fatal("diagnostics exported the access key")
			}
		})
	}
}

func TestDiagnosticsLoopbackAndRemoteSource(t *testing.T) {
	remoteLogger := diagnostics.New(filepath.Join(t.TempDir(), "remote"), nil, diagnostics.Hosted, true)
	remoteLogger.Record(diagnostics.XMPPAuthFailed, 0, 1)
	remote, err := New("http://127.0.0.1:1", WithDiagnostics(remoteLogger, "private-key-test-sentinel", DiagnosticsKeyRequired))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(remote)
	defer upstream.Close()
	localLogger := diagnostics.New(filepath.Join(t.TempDir(), "local"), nil, diagnostics.Desktop, false)
	localLogger.Record(diagnostics.Ready, 0, 0)
	native, err := New(upstream.URL, WithDiagnostics(localLogger, "", DiagnosticsNative), WithRemoteDiagnostics(upstream.URL))
	if err != nil {
		t.Fatal(err)
	}
	local, err := New("http://127.0.0.1:1", WithDiagnostics(localLogger, "", DiagnosticsLoopback))
	if err != nil {
		t.Fatal(err)
	}
	query := func(s http.Handler, host, source, key, via string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://%s%s?source=%s", host, DiagnosticsPath, source), nil)
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set(diagnosticsMarker, "1")
		r.Header.Set(diagnosticsKey, key)
		r.Header.Set("X-Forwarded-For", via)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if got := query(local, "127.0.0.1", "local", "", ""); got.Code != 200 {
		t.Fatal("direct local access failed", got.Code)
	}
	for _, got := range []*httptest.ResponseRecorder{query(local, "public.example", "local", "", ""), query(local, "localhost", "local", "", "192.0.2.4"), query(native, "localhost", "server", "", "")} {
		if got.Code != 403 {
			t.Fatal("untrusted access allowed", got.Code)
		}
	}
	if got := query(native, "localhost", "local", "", ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"event":"ready"`) {
		t.Fatal("desktop local diagnostics not available")
	}
	if got := query(native, "localhost", "server", "private-key-test-sentinel", ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"event":"xmpp_authentication_failed"`) {
		t.Fatal("hosted diagnostics not available in desktop", got.Code)
	}
	malicious := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", diagnosticsType)
		_, _ = fmt.Fprintln(w, `{"message":"message-content-sentinel","file":"private-file.pdf"}`)
	}))
	defer malicious.Close()
	native, err = New(malicious.URL, WithDiagnostics(localLogger, "", DiagnosticsNative), WithRemoteDiagnostics(malicious.URL))
	if err != nil {
		t.Fatal(err)
	}
	if got := query(native, "localhost", "server", "", ""); got.Code != 503 || got.Body.Len() != 0 {
		t.Fatal("unrecognized upstream data reached diagnostics")
	}

	upstream.Close()
	if got := query(native, "localhost", "local", "", ""); got.Code != 200 {
		t.Fatal("device records should be accessible without the hosted engine")
	}
}
