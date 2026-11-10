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
	"path/filepath"
	"sync"
	"time"

	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/oie/api"
	"github.com/araihu/oie/internal/adapters/avatarfiles"
	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/adapters/previewfetch"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/adapters/videothumb"
	"github.com/araihu/oie/internal/adapters/web"
	xmpadapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

const capabilityHeader = "X-Oie-Host-Capability"

// Config selects existing engine/presentation roles. PrivateAPI isolates a
// native host's internal HTTP listener; its presentation Handler stays available
// to the native asset server without exposing the structured engine API.
// DataOnly omits presentation and assets for local/hosted network engines.
type Config struct {
	Role, Listen, DataDir, EngineURL, FFmpeg, XMPPCAFile string
	DevReload                                            bool
	PrivateAPI                                           bool
	DataOnly                                             bool
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
	var protocols *xmpadapter.Manager
	var workerDone <-chan error
	defer func() {
		if err == nil {
			return
		}
		cancel()
		_ = listener.Close()
		if workerDone != nil {
			<-workerDone
		}
		if protocols != nil {
			protocols.Close()
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
		engine, newErr := application.New(store, cfg.Role, newID, time.Now)
		if newErr != nil {
			return nil, newErr
		}
		engine.SetAvatarFiles(avatarfiles.New(filepath.Join(cfg.DataDir, "media", "avatars")))
		fetcher := previewfetch.New()
		engine.SetPreviewServices(fetcher, mediafiles.New(filepath.Join(cfg.DataDir, "media", "images")))
		engine.SetMediaReader(fetcher)
		engine.SetVideoThumbnailer(videothumb.New(cfg.FFmpeg))
		roots, rootErr := certificateRoots(cfg.XMPPCAFile)
		if rootErr != nil {
			return nil, rootErr
		}
		protocols = xmpadapter.New(filepath.Join(cfg.DataDir, "private", "connections"), roots)
		engine.SetProtocols(protocols)
		if err = protocols.Start(ctx, engine); err != nil {
			return nil, err
		}
		handler, handlerErr := httpapi.New(engine)
		if handlerErr != nil {
			return nil, handlerErr
		}
		apiMux.Handle("/api/v1/", handler)
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
		result := make(chan error, 1)
		workerDone = result
		go func() { result <- engine.Run(ctx) }()
	}
	host.presentation = host.track(http.NotFoundHandler())
	if !cfg.DataOnly {
		ui, err := web.New(engineURL, options...)
		if err != nil {
			return nil, err
		}
		presentation := http.NewServeMux()
		presentation.Handle("GET /assets/", assets.Handler())
		presentation.HandleFunc("/api/", http.NotFound)
		presentation.Handle("/", ui)
		host.presentation = host.track(presentation)
		apiMux.Handle("/", presentation)
	}
	networkHandler := http.Handler(apiMux)
	if cfg.PrivateAPI {
		networkHandler = host.guard(networkHandler)
	}
	host.server = &http.Server{Handler: host.track(networkHandler), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	served := make(chan error, 1)
	go func() { served <- host.server.Serve(listener) }()
	go host.supervise(served, workerDone, protocols, store)
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
	if cfg.DataOnly && (cfg.Role == "bff" || cfg.PrivateAPI) {
		return errors.New("data-only requires a local or hosted public engine listener")
	}
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

func (s *Server) supervise(served, workerDone <-chan error, protocols *xmpadapter.Manager, store *sqlite.Store) {
	var result error
	workerConsumed := false
	select {
	case result = <-served:
		if errors.Is(result, http.ErrServerClosed) {
			result = nil
		}
	case result = <-workerDone:
		workerConsumed = true
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
	if workerDone != nil && !workerConsumed {
		if err := <-workerDone; err != nil && !errors.Is(err, context.Canceled) {
			result = errors.Join(result, fmt.Errorf("engine worker: %w", err))
		}
	}
	if protocols != nil {
		protocols.Close()
	}
	if store != nil {
		result = errors.Join(result, store.Close())
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
