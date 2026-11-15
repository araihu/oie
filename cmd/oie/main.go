package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/araihu/oie/internal/apphost"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, flag.ErrHelp) {
		slog.Error("oie stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "auth" {
		return runAuth(ctx, os.Args[2:], os.Stdout)
	}
	var hosted apphost.HostedConfig
	flag.StringVar(&hosted.PublicURL, "public-url", "", "public HTTPS origin for hosted mode")
	flag.StringVar(&hosted.TLSCertFile, "tls-cert-file", "", "hosted TLS certificate PEM")
	flag.StringVar(&hosted.TLSKeyFile, "tls-key-file", "", "hosted TLS private key PEM")
	flag.StringVar(&hosted.TrustedTLSProxies, "trusted-tls-proxies", "", "private TLS-terminating proxy CIDRs, comma-separated")
	flag.StringVar(&hosted.OIDCClientID, "oidc-client-id", "", "hosted OIDC client ID")
	flag.StringVar(&hosted.OIDCClientSecretFile, "oidc-client-secret-file", "", "file containing the hosted OIDC client secret")
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	role := flag.String("role", "local", "local, hosted, or bff")
	dataOnly := flag.Bool("data-only", false, "serve the engine API without the web interface")
	dataDir := flag.String("data-dir", "./data", "engine database and media directory")
	engineURL := flag.String("engine-url", "", "engine base URL (required for bff role)")
	devReload := flag.Bool("dev-reload", false, "reload the development browser after BFF restarts (bff role only)")
	xmppCA := flag.String("xmpp-ca-file", "", "additional trusted CA certificate PEM for XMPP TLS (development/private servers)")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments; see oie --help")
	}
	server, err := apphost.Start(ctx, apphost.Config{Role: *role, DataOnly: *dataOnly, Listen: *listen, DataDir: *dataDir, EngineURL: *engineURL, DevReload: *devReload, XMPPCAFile: *xmppCA, Hosted: hosted})
	if err != nil {
		return err
	}
	slog.Info("oie listening", "address", server.URL(), "role", *role)
	select {
	case err = <-server.Done():
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Close(shutdownCtx)
	}
}
