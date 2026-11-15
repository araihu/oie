package main

import (
	"context"
	"errors"
	"flag"
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
	flags := flag.NewFlagSet("oie", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	role := flags.String("role", "local", "local, hosted, or bff")
	dataOnly := flags.Bool("data-only", false, "serve the engine API without the web interface")
	dataDir := flags.String("data-dir", "./data", "engine database and media directory")
	engineURL := flags.String("engine-url", "", "engine base URL (required for bff role)")
	devReload := flags.Bool("dev-reload", false, "reload the development browser after BFF restarts (bff role only)")
	xmppCA := flags.String("xmpp-ca-file", "", "additional trusted CA certificate PEM for XMPP TLS (development/private servers)")
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	server, err := apphost.Start(ctx, apphost.Config{
		Role: *role, DataOnly: *dataOnly, Listen: *listen, DataDir: *dataDir,
		EngineURL: *engineURL, DevReload: *devReload, XMPPCAFile: *xmppCA,
		Diagnostics: logger, DiagnosticsKey: os.Getenv("OIE_DIAGNOSTICS_KEY"),
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
