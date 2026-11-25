package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/araihu/oie/internal/apphost"
	"github.com/araihu/oie/internal/diagnostics"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if len(os.Args) > 1 {
		var err error
		switch os.Args[1] {
		case "omemo":
			err = runOMEMO(ctx, os.Args[2:], os.Stdout)
		case "auth":
			err = runAuth(ctx, os.Args[2:], os.Stdout)
		case "backup":
			err = runBackup(ctx, os.Args[2:], os.Stdout, os.Stderr)
		default:
			return runServer(ctx)
		}
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		return err
	}
	return runServer(ctx)
}

func runServer(ctx context.Context) error {
	var hosted apphost.HostedConfig
	flags := flag.NewFlagSet("oie", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&hosted.PublicURL, "public-url", os.Getenv("OIE_PUBLIC_URL"), "public HTTPS origin for hosted mode")
	flags.StringVar(&hosted.TLSCertFile, "tls-cert-file", os.Getenv("OIE_TLS_CERT_FILE"), "hosted TLS certificate PEM")
	flags.StringVar(&hosted.TLSKeyFile, "tls-key-file", os.Getenv("OIE_TLS_KEY_FILE"), "hosted TLS private key PEM")
	flags.StringVar(&hosted.TrustedTLSProxies, "trusted-tls-proxies", os.Getenv("OIE_TRUSTED_TLS_PROXIES"), "private TLS-terminating proxy CIDRs, comma-separated")
	flags.StringVar(&hosted.OIDCClientID, "oidc-client-id", os.Getenv("OIE_OIDC_CLIENT_ID"), "hosted OIDC client ID")
	flags.StringVar(&hosted.OIDCClientSecretFile, "oidc-client-secret-file", os.Getenv("OIE_OIDC_CLIENT_SECRET_FILE"), "file containing the hosted OIDC client secret")
	listen := flags.String("listen", envOrDefault("OIE_LISTEN", "127.0.0.1:8080"), "HTTP listen address")
	probeListen := flags.String("probe-listen", os.Getenv("OIE_PROBE_LISTEN"), "private HTTP probe listener address (disabled by default)")
	role := flags.String("role", envOrDefault("OIE_ROLE", "local"), "local, hosted, or bff")
	dataOnly := flags.Bool("data-only", false, "serve the engine API without the web interface")
	dataDir := flags.String("data-dir", envOrDefault("OIE_DATA_DIR", "./data"), "engine database and media directory")
	engineURL := flags.String("engine-url", "", "engine base URL (required for bff role)")
	devReload := flags.Bool("dev-reload", false, "reload the development browser after BFF restarts (bff role only)")
	xmppCA := flags.String("xmpp-ca-file", "", "additional trusted CA certificate PEM for XMPP TLS (development/private servers)")
	omemoUploadOrigin := flags.String("omemo-upload-origin", os.Getenv("OIE_OMEMO_UPLOAD_ORIGIN"), "allow encrypted attachment downloads from one private HTTPS upload origin")
	logDir := flags.String("diagnostics-dir", "", "private diagnostics directory (defaults to diagnostics under the data directory)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return nil
		}
		diagnostics.New("", os.Stderr, diagnostics.Local, false).Record(diagnostics.ConfigFailed, 0, 0)
		return err
	}
	if flags.NArg() != 0 {
		diagnostics.New("", os.Stderr, diagnostics.Local, false).Record(diagnostics.ConfigFailed, 0, 0)
		return errors.New("unexpected arguments")
	}
	if *dataDir == "" {
		*dataDir = "./data"
	}
	if *logDir == "" {
		*logDir = filepath.Join(*dataDir, "diagnostics")
	}
	mode := diagnostics.Mode(*role)
	host, _, _ := net.SplitHostPort(*listen)
	if mode == diagnostics.Local && host != "localhost" && !net.ParseIP(host).IsLoopback() {
		mode = diagnostics.Hosted
	}
	logger := diagnostics.New(*logDir, os.Stderr, mode, diagnostics.IsDebug())
	diagnostics.UseStandardLogger(logger)
	server, err := apphost.Start(ctx, apphost.Config{
		Role: *role, DataOnly: *dataOnly, Listen: *listen, DataDir: *dataDir,
		EngineURL: *engineURL, DevReload: *devReload, XMPPCAFile: *xmppCA, PrivateOMEMOUploadOrigin: *omemoUploadOrigin,
		Hosted: hosted, ProbeListen: *probeListen, Diagnostics: logger, DiagnosticsKey: os.Getenv("OIE_DIAGNOSTICS_KEY"),
	})
	if err != nil {
		return err
	}
	select {
	case err = <-server.Done():
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = server.Close(shutdownCtx); err != nil {
			logger.Record(diagnostics.ShutdownFailed, 0, 0)
		}
		return err
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
