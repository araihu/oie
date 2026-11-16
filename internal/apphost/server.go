// Package apphost composes the engine and presentation adapters for HTTP and
// native window hosts. Application rules remain in the application package.
package apphost

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/oie/api"
	"github.com/araihu/oie/internal/adapters/browserpush"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/diagnostics"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

const capabilityHeader = "X-Oie-Host-Capability"

// Config selects existing engine/presentation roles. PrivateAPI isolates a
// native host's internal HTTP listener; its presentation Handler stays available
// to the native asset server without exposing the structured engine API.
// DataOnly omits presentation and assets for local/hosted network engines.
type Config struct {
	Role, Listen, DataDir, EngineURL, XMPPCAFile string
	DevReload                                    bool
	PrivateAPI                                   bool
	DataOnly                                     bool
	Hosted                                       HostedConfig
	ProbeListen                                  string
	Diagnostics                                  *diagnostics.Logger
	DiagnosticsKey                               string
	engineOnly                                   bool
	browserPush                                  *browserpush.Service
}

// Server owns one engine, optional protocol sessions, the BFF, and their shared
// lifetime. Hiding a native window does not close this server.
type Server struct {
	url          string
	ctx          context.Context
	cancel       context.CancelFunc
	server       *http.Server
	private      *http.Server
	probe        *http.Server
	presentation http.Handler
	done         chan error
	stopped      chan struct{}
	err          error
	capability   string
	requestMu    sync.Mutex
	closing      bool
	requests     sync.WaitGroup
}

func newID() (domain.ID, error) {
	id, err := uuid.NewV7()
	return domain.ID(id.String()), err
}

// Start opens the selected data directory and listener and starts the owned
// workers. On failure it closes every resource opened during this attempt.
func Start(parent context.Context, cfg Config) (_ *Server, err error) {
	if err = parent.Err(); err != nil {
		return nil, err
	}
	cfg = defaults(cfg)
	cfg.Diagnostics.Record(diagnostics.Starting, 0, 0)
	if err = validate(cfg); err != nil {
		cfg.Diagnostics.Record(diagnostics.ConfigFailed, 0, 0)
		return nil, err
	}
	var hosted *hostedServer
	if cfg.Role == "hosted" {
		hosted, err = configureHosted(cfg)
		if err != nil {
			cfg.Diagnostics.Record(diagnostics.ConfigFailed, 0, 0)
			return nil, err
		}
	}
	if hosted != nil && !cfg.DataOnly {
		cfg.browserPush = browserpush.New(hosted.auth, hosted.origin.String())
	}
	ctx, cancel := context.WithCancel(parent)
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		cancel()
		cfg.Diagnostics.Record(diagnostics.ListenFailed, 0, 0)
		return nil, err
	}
	var store *sqlite.Store
	var engine *engineHost
	var privateListener, probeListener net.Listener
	defer func() {
		if err == nil {
			return
		}
		cancel()
		_ = listener.Close()
		if privateListener != nil {
			_ = privateListener.Close()
		}
		if probeListener != nil {
			_ = probeListener.Close()
		}
		if engine != nil {
			engine.instance.close()
		}
		if store != nil {
			err = errors.Join(err, store.Close())
		}
	}()
	if cfg.ProbeListen != "" {
		probeListener, err = net.Listen("tcp", cfg.ProbeListen)
		if err != nil {
			cfg.Diagnostics.Record(diagnostics.ListenFailed, 0, 0)
			return nil, fmt.Errorf("listen for internal probes: %w", err)
		}
	}
	host := &Server{url: listenerURL(listener), ctx: ctx, cancel: cancel, done: make(chan error, 1), stopped: make(chan struct{})}
	if cfg.PrivateAPI || (hosted != nil && !cfg.DataOnly) {
		host.capability = rand.Text()
	}
	apiMux := http.NewServeMux()
	engineURL := cfg.EngineURL
	var options []web.Option
	var hostedDiagnostics http.Handler
	if cfg.Diagnostics != nil {
		addr := listener.Addr().(*net.TCPAddr)
		access := web.DiagnosticsKeyRequired
		if cfg.Role == "local" && addr.IP.IsLoopback() {
			access = web.DiagnosticsLoopback
		}
		if hosted != nil {
			hostedDiagnostics = web.DiagnosticsHandler(cfg.Diagnostics, "", web.DiagnosticsAuthenticated)
			options = append(options, web.WithDiagnostics(cfg.Diagnostics, "", web.DiagnosticsAuthenticated))
		} else if cfg.DataOnly {
			apiMux.Handle(web.DiagnosticsPath, web.DiagnosticsHandler(cfg.Diagnostics, cfg.DiagnosticsKey, access))
		} else {
			options = append(options, web.WithDiagnostics(cfg.Diagnostics, cfg.DiagnosticsKey, access))
		}
	}
	if cfg.DevReload {
		options = append(options, web.WithDevReload())
	}
	if cfg.Role != "bff" {
		candidateID, idErr := newID()
		if idErr != nil {
			return nil, idErr
		}
		store, err = sqlite.Open(ctx, cfg.DataDir, candidateID, sqlite.WithMessageSearchIndex(), sqlite.WithDiagnostics(cfg.Diagnostics))
		if err != nil {
			cfg.Diagnostics.Record(diagnostics.StorageFailed, 0, 0)
			return nil, err
		}
		engine, err = newEngineHost(ctx, cfg, store)
		if err != nil {
			cfg.Diagnostics.Record(diagnostics.EngineFailed, 0, 0)
			return nil, err
		}
		apiMux.Handle("/api/v1/", engine)
		apiMux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(api.OpenAPI)
		})
		engineURL = host.url
		if hosted != nil && !cfg.DataOnly {
			privateListener, err = net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			engineURL = listenerURL(privateListener)
		}
		if host.capability != "" {
			options = append(options, web.WithEngineRequestEditor(host.editEngineRequest))
		}
	}
	host.presentation = host.track(http.NotFoundHandler())
	var browser http.Handler
	if !cfg.DataOnly && !cfg.engineOnly {
		if hosted != nil {
			options = append(options, web.WithHostedSignIn(hosted.auth), web.WithBrowserPush(hosted.auth, cfg.browserPush))
		}
		ui, err := web.New(engineURL, options...)
		if err != nil {
			cfg.Diagnostics.Record(diagnostics.ConfigFailed, 0, 0)
			return nil, err
		}
		if hosted != nil {
			browser = hosted.auth.Browser(ui, ui.SignIn)
		} else {
			presentation := presentationHandler(ui)
			host.presentation = host.track(presentation)
			apiMux.Handle("/", presentation)
		}
	}
	networkHandler := http.Handler(apiMux)
	if hosted != nil {
		networkHandler = hosted.handler(apiMux, browser, hostedDiagnostics)
		if privateListener != nil {
			host.private = host.httpServer(host.guard(apiMux), cfg.Diagnostics)
		}
	} else if cfg.PrivateAPI {
		networkHandler = host.guard(networkHandler)
	}
	host.server = host.httpServer(networkHandler, cfg.Diagnostics)
	if hosted != nil {
		host.url = hosted.origin.String()
		host.server.TLSConfig = hosted.tls
	}
	served := make(chan error, 3)
	if probeListener != nil {
		host.probe = host.httpServer(probeHandler(engine), cfg.Diagnostics)
		host.probe.Addr = probeListener.Addr().String()
		go func() { served <- host.probe.Serve(probeListener) }()
	}
	if host.private != nil {
		go func() { served <- host.private.Serve(privateListener) }()
	}
	go func() {
		if hosted != nil && hosted.tls != nil {
			served <- host.server.ServeTLS(listener, "", "")
		} else {
			served <- host.server.Serve(listener)
		}
	}()
	cfg.Diagnostics.Record(diagnostics.Ready, 0, 0)
	go host.supervise(served, engine, cfg.Diagnostics)
	return host, nil
}

func (s *Server) httpServer(handler http.Handler, diag *diagnostics.Logger) *http.Server {
	return &http.Server{
		ErrorLog: diag.StandardLogger(), Handler: s.track(handler), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
		BaseContext: func(net.Listener) context.Context { return s.ctx },
	}
}

func presentationHandler(ui http.Handler) http.Handler {
	presentation := http.NewServeMux()
	presentation.Handle("GET /assets/", assets.Handler())
	presentation.HandleFunc("/api/", http.NotFound)
	presentation.Handle("/", ui)
	return presentation
}

func (s *Server) editEngineRequest(_ context.Context, r *http.Request) error {
	r.Header.Set(capabilityHeader, s.capability)
	return nil
}

func defaults(cfg Config) Config {
	if cfg.Role == "" {
		cfg.Role = "local"
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8080"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	return cfg
}

func validate(cfg Config) error {
	if cfg.DataOnly && (cfg.Role == "bff" || cfg.PrivateAPI) {
		return errors.New("data-only requires a local or hosted public engine listener")
	}
	if cfg.Role != "local" && cfg.Role != "hosted" && cfg.Role != "bff" {
		return errors.New("role must be local, hosted, or bff; sync service is not implemented yet")
	}
	if cfg.ProbeListen != "" && (cfg.Role == "bff" || cfg.PrivateAPI) {
		return errors.New("--probe-listen requires a local or hosted network engine")
	}
	if cfg.Role == "hosted" && cfg.PrivateAPI {
		return errors.New("hosted mode uses its own authenticated API")
	}
	if cfg.Role != "hosted" && cfg.Hosted != (HostedConfig{}) {
		return errors.New("hosted TLS and OIDC options require --role hosted")
	}
	if cfg.DevReload && cfg.Role != "bff" {
		return errors.New("dev-reload is only supported by the bff role")
	}
	if cfg.Role == "bff" && cfg.EngineURL == "" {
		return errors.New("bff role requires engine-url")
	}
	if cfg.Role != "bff" && cfg.EngineURL != "" {
		return errors.New("engine-url is only used by the bff role")
	}
	return nil
}

func listenerURL(listener net.Listener) string {
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func certificateRoots(path string) (*x509.CertPool, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if path != "" {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("xmpp-ca-file contains no valid certificates")
		}
	}
	return roots, nil
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(capabilityHeader)), []byte(s.capability)) != 1 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requestMu.Lock()
		if s.closing || s.ctx.Err() != nil {
			s.requestMu.Unlock()
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		s.requests.Add(1)
		s.requestMu.Unlock()
		defer s.requests.Done()
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) beginClose() {
	s.requestMu.Lock()
	s.closing = true
	s.requestMu.Unlock()
	s.cancel()
}

func (s *Server) supervise(served <-chan error, engine *engineHost, diag *diagnostics.Logger) {
	var result error
	var failures <-chan error
	if engine != nil {
		failures = engine.failures
	}
	select {
	case result = <-served:
		if errors.Is(result, http.ErrServerClosed) {
			result = nil
		}
	case result = <-failures:
		if result != nil && !errors.Is(result, context.Canceled) {
			diag.Record(diagnostics.WorkerFailed, 0, 0)
			result = fmt.Errorf("engine worker: %w", result)
		} else {
			result = nil
		}
	case <-s.ctx.Done():
	}
	s.beginClose()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	shutdownErr := shutdownHTTP(shutdownCtx, s.server, s.private, s.probe)
	stop()
	if shutdownErr != nil {
		result = errors.Join(result, fmt.Errorf("shutdown: %w", shutdownErr))
	}
	s.requests.Wait()
	if engine != nil {
		if engine.instance != nil {
			engine.instance.close()
		}
		result = errors.Join(result, engine.store.Close())
	}
	if result != nil {
		diag.Record(diagnostics.ShutdownFailed, 0, 0)
	} else {
		diag.Record(diagnostics.Stopped, 0, 0)
	}
	s.err = result
	s.done <- result
	close(s.done)
	close(s.stopped)
}

func shutdownHTTP(ctx context.Context, servers ...*http.Server) error {
	results := make(chan error, len(servers))
	for _, server := range servers {
		if server == nil {
			results <- nil
			continue
		}
		// Start draining each listener before another can consume the deadline.
		go func() {
			err := server.Shutdown(ctx)
			if err != nil {
				err = errors.Join(err, server.Close())
			}
			results <- err
		}()
	}
	var result error
	for range servers {
		result = errors.Join(result, <-results)
	}
	return result
}

// URL is the owned HTTP listener's URL. In PrivateAPI mode it is an internal
// transport and is not the native window's navigation URL.
func (s *Server) URL() string { return s.url }

// Handler serves presentation/assets only, with the owned lifetime attached to
// every request. It is suitable for mounting in a native asset server.
func (s *Server) Handler() http.Handler { return s.presentation }

// Done reports the final result after requests, workers and storage have closed.
func (s *Server) Done() <-chan error { return s.done }

// Close cancels long-lived requests before draining HTTP and engine workers.
// Concurrent calls wait for the same cleanup; a caller deadline does not close
// storage while another request still uses it.
func (s *Server) Close(ctx context.Context) error {
	s.beginClose()
	select {
	case <-s.stopped:
		return s.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
