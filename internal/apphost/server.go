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
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/adapters/web"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

const capabilityHeader = "X-Oie-Host-Capability"

// Config selects existing engine/presentation roles. PrivateAPI isolates a
// native host's internal HTTP listener; its presentation Handler stays available
// to the native asset server without exposing the structured engine API.
type Config struct {
	Role, Listen, DataDir, EngineURL, FFmpeg, XMPPCAFile string
	DevReload                                            bool
	PrivateAPI                                           bool
}

// Server owns one engine, optional protocol sessions, the BFF, and their shared
// lifetime. Hiding a native window does not close this server.
type Server struct {
	url          string
	ctx          context.Context
	cancel       context.CancelFunc
	server       *http.Server
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
	if err = validate(cfg); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		cancel()
		return nil, err
	}
	var store *sqlite.Store
	var engine *engineHost
	defer func() {
		if err == nil {
			return
		}
		cancel()
		_ = listener.Close()
		if engine != nil {
			engine.instance.close()
		}
		if store != nil {
			err = errors.Join(err, store.Close())
		}
	}()
	host := &Server{url: listenerURL(listener), ctx: ctx, cancel: cancel, done: make(chan error, 1), stopped: make(chan struct{})}
	if cfg.PrivateAPI {
		host.capability = rand.Text()
	}
	apiMux := http.NewServeMux()
	engineURL := cfg.EngineURL
	var options []web.Option
	if cfg.DevReload {
		options = append(options, web.WithDevReload())
	}
	if cfg.Role != "bff" {
		candidateID, idErr := newID()
		if idErr != nil {
			return nil, idErr
		}
		store, err = sqlite.Open(ctx, cfg.DataDir, candidateID)
		if err != nil {
			return nil, err
		}
		engine, err = newEngineHost(ctx, cfg, store)
		if err != nil {
			return nil, err
		}
		apiMux.Handle("/api/v1/", engine)
		apiMux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(api.OpenAPI)
		})
		engineURL = host.url
		if cfg.PrivateAPI {
			options = append(options, web.WithEngineRequestEditor(func(_ context.Context, r *http.Request) error {
				r.Header.Set(capabilityHeader, host.capability)
				return nil
			}))
		}
	}
	ui, err := web.New(engineURL, options...)
	if err != nil {
		return nil, err
	}
	presentation := http.NewServeMux()
	presentation.Handle("GET /assets/", assets.Handler())
	// These paths belong to the BFF despite sharing the engine API prefix. Keep
	// its CSRF, account/engine binding and response validation for native clients.
	for _, pattern := range []string{
		"GET /api/v1/previews/{id}",
		"GET /api/v1/shared-content/{id}/thumbnail",
		"GET /api/v1/conversations/{id}/read-state",
		"POST /api/v1/conversations/{id}/read-state",
	} {
		presentation.Handle(pattern, ui)
	}
	presentation.HandleFunc("/api/", http.NotFound)
	presentation.Handle("/", ui)
	host.presentation = host.track(presentation)
	apiMux.Handle("/", presentation)
	networkHandler := http.Handler(apiMux)
	if cfg.PrivateAPI {
		networkHandler = host.guard(networkHandler)
	}
	host.server = &http.Server{Handler: host.track(networkHandler), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	served := make(chan error, 1)
	go func() { served <- host.server.Serve(listener) }()
	go host.supervise(served, engine)
	return host, nil
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
	if cfg.FFmpeg == "" {
		cfg.FFmpeg = "ffmpeg"
	}
	return cfg
}

func validate(cfg Config) error {
	if cfg.Role != "local" && cfg.Role != "hosted" && cfg.Role != "bff" {
		return errors.New("role must be local, hosted, or bff; sync service is not implemented yet")
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

func (s *Server) supervise(served <-chan error, engine *engineHost) {
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
			result = fmt.Errorf("engine worker: %w", result)
		} else {
			result = nil
		}
	case <-s.ctx.Done():
	}
	s.beginClose()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	shutdownErr := s.server.Shutdown(shutdownCtx)
	stop()
	if shutdownErr != nil {
		result = errors.Join(result, fmt.Errorf("shutdown: %w", shutdownErr), s.server.Close())
	}
	s.requests.Wait()
	if engine != nil {
		if engine.instance != nil {
			engine.instance.close()
		}
		result = errors.Join(result, engine.store.Close())
	}
	s.err = result
	s.done <- result
	close(s.done)
	close(s.stopped)
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
