package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/oie/api"
	"github.com/araihu/oie/internal/adapters/avatarfiles"
	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/adapters/previewfetch"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/adapters/web"
	xmpadapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func newID() (domain.ID, error) {
	id, err := uuid.NewV7()
	return domain.ID(id.String()), err
}

func main() {
	if err := run(); err != nil {
		slog.Error("oie stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	role := flag.String("role", "local", "local, hosted, or bff")
	dataDir := flag.String("data-dir", "./data", "engine database and media directory")
	engineURL := flag.String("engine-url", "", "engine base URL (required for bff role)")
	xmppCA := flag.String("xmpp-ca-file", "", "additional trusted CA certificate PEM for XMPP TLS (development/private servers)")
	flag.Parse()
	if *role != "local" && *role != "hosted" && *role != "bff" {
		return errors.New("role must be local, hosted, or bff; sync service is not implemented yet")
	}
	if *role == "bff" && *engineURL == "" {
		return errors.New("bff role requires engine-url")
	}
	if *role != "bff" && *engineURL != "" {
		return errors.New("engine-url is only used by the bff role")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	var engineDone <-chan error
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", assets.Handler())
	if *role != "bff" {
		candidateID, err := newID()
		if err != nil {
			return err
		}
		store, err := sqlite.Open(ctx, *dataDir, candidateID)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		engine, err := application.New(store, *role, newID, time.Now)
		if err != nil {
			return err
		}
		engine.SetAvatarFiles(avatarfiles.New(filepath.Join(*dataDir, "media", "avatars")))
		fetcher := previewfetch.New()
		engine.SetPreviewServices(fetcher, mediafiles.New(filepath.Join(*dataDir, "media", "images")))
		engine.SetMediaReader(fetcher)
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if *xmppCA != "" {
			pem, err := os.ReadFile(*xmppCA)
			if err != nil {
				return err
			}
			if !roots.AppendCertsFromPEM(pem) {
				return errors.New("xmpp-ca-file contains no valid certificates")
			}
		}
		protocols := xmpadapter.New(filepath.Join(*dataDir, "private", "connections"), roots)
		engine.SetProtocols(protocols)
		defer protocols.Close()
		if err := protocols.Start(ctx, engine); err != nil {
			return err
		}
		workerResult := make(chan error, 1)
		workerStopped := make(chan struct{})
		engineDone = workerResult
		go func() { defer close(workerStopped); workerResult <- engine.Run(ctx) }()
		defer func() { stop(); <-workerStopped }()
		handler, err := httpapi.New(engine)
		if err != nil {
			return err
		}
		mux.Handle("/api/v1/", handler)
		mux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(api.OpenAPI)
		})
		host, port, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			return err
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		*engineURL = "http://" + net.JoinHostPort(host, port)
	}
	ui, err := web.New(*engineURL)
	if err != nil {
		return err
	}
	mux.Handle("/", ui)
	server := &http.Server{
		Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	defer func() { _ = server.Close() }()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	slog.Info("oie listening", "address", listener.Addr(), "role", *role)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case err := <-engineDone:
		stop()
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("engine worker: %w", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}
