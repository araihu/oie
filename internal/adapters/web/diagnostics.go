package web

import (
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/diagnostics"
)

const DiagnosticsPath = "/ui/diagnostics/log"
const diagnosticsMarker = "X-Oie-Diagnostics"
const diagnosticsKey = "X-Oie-Diagnostics-Key"
const diagnosticsTruncated = "X-Oie-Diagnostics-Truncated"
const diagnosticsType = "application/x-ndjson"

//go:embed diagnostics.js
var diagnosticsScript []byte

type DiagnosticsAccess int

const (
	DiagnosticsKeyRequired DiagnosticsAccess = iota
	DiagnosticsLoopback
	DiagnosticsNative
)

type diagnosticsView struct {
	log           *diagnostics.Logger
	keyHash       [32]byte
	keyConfigured bool
	keyEnabled    bool
	access        DiagnosticsAccess
	remote        *url.URL
	remoteClient  *http.Client
}

func WithDiagnostics(logger *diagnostics.Logger, key string, access DiagnosticsAccess) Option {
	return func(s *Server) {
		if logger != nil {
			s.diagnostics = newDiagnosticsView(logger, key, access)
		}
	}
}

// DiagnosticsHandler lets a data-only engine provide the same protected file
// snapshot to desktop without starting a browser frontend.
func DiagnosticsHandler(logger *diagnostics.Logger, key string, access DiagnosticsAccess) http.Handler {
	return newDiagnosticsView(logger, key, access)
}

func newDiagnosticsView(logger *diagnostics.Logger, key string, access DiagnosticsAccess) *diagnosticsView {
	return &diagnosticsView{
		log: logger, keyHash: sha256.Sum256([]byte(key)), access: access,
		keyConfigured: key != "", keyEnabled: len(key) >= 16 && len(key) <= 512,
	}
}

// WithRemoteDiagnostics enables the hosted source in the native Diagnostics
// drawer. Credentials go only to the configured HTTPS or loopback engine.
func WithRemoteDiagnostics(engine string) Option {
	return func(s *Server) {
		if s.diagnostics == nil || s.diagnostics.access != DiagnosticsNative || engine == "" {
			return
		}
		target, err := url.Parse(engine)
		if err != nil || !validEngineURL(engine) || (target.Scheme != "https" && !loopbackHost(target.Host)) {
			return
		}
		s.diagnostics.remote = target.JoinPath(DiagnosticsPath)
		s.diagnostics.remoteClient = &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
}

func loopbackHost(host string) bool {
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return strings.EqualFold(host, "localhost") || ip.IsLoopback()
}

func (d *diagnosticsView) authorized(r *http.Request) bool {
	if r.Header.Get(diagnosticsMarker) != "1" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	if d.access == DiagnosticsNative {
		return true
	}
	if d.keyConfigured {
		key := sha256.Sum256([]byte(r.Header.Get(diagnosticsKey)))
		return d.keyEnabled && subtle.ConstantTimeCompare(d.keyHash[:], key[:]) == 1
	}
	// A public listener or hosted role always requires the access key. A
	// loopback listener also requires it when used through a reverse proxy.
	if d.access != DiagnosticsLoopback || !loopbackHost(r.Host) ||
		r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" ||
		r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(peer).IsLoopback()
}

func (d *diagnosticsView) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if d == nil || d.log == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !d.authorized(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	query := r.URL.Query()
	hours := 24
	if query.Has("hours") {
		value, err := strconv.Atoi(query.Get("hours"))
		if err != nil || (value != 1 && value != 24 && value != 168) || len(query["hours"]) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hours = value
	}
	var snapshot []byte
	var truncated bool
	switch query.Get("source") {
	case "", "local":
		var err error
		snapshot, truncated, err = d.log.Snapshot(hours)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	case "server":
		var status int
		snapshot, truncated, status = d.readRemote(r, hours)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", diagnosticsType)
	if truncated {
		w.Header().Set(diagnosticsTruncated, "1")
	}
	_, _ = w.Write(snapshot)
}

func (d *diagnosticsView) readRemote(r *http.Request, hours int) ([]byte, bool, int) {
	if d.remote == nil {
		return nil, false, http.StatusNotFound
	}
	target := *d.remote
	target.RawQuery = url.Values{"hours": {strconv.Itoa(hours)}}.Encode()
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, false, http.StatusServiceUnavailable
	}
	request.Header.Set(diagnosticsMarker, "1")
	request.Header.Set(diagnosticsKey, r.Header.Get(diagnosticsKey))
	response, err := d.remoteClient.Do(request)
	if err != nil {
		return nil, false, http.StatusServiceUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusForbidden, http.StatusUnauthorized:
		return nil, false, http.StatusForbidden
	case http.StatusOK:
		if response.Header.Get("Content-Type") != diagnosticsType {
			return nil, false, http.StatusServiceUnavailable
		}
	default:
		return nil, false, http.StatusServiceUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, diagnostics.MaxSnapshotBytes+1))
	if err != nil || (len(data) != 0 && !diagnostics.ValidSnapshot(string(data))) {
		return nil, false, http.StatusServiceUnavailable
	}
	return data, response.Header.Get(diagnosticsTruncated) == "1", http.StatusOK
}
