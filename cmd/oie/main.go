package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/araihu/oie/internal/apphost"
)

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
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "FFmpeg executable for optional video thumbnails")
	devReload := flag.Bool("dev-reload", false, "reload the development browser after BFF restarts (bff role only)")
	xmppCA := flag.String("xmpp-ca-file", "", "additional trusted CA certificate PEM for XMPP TLS (development/private servers)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	server, err := apphost.Start(ctx, apphost.Config{Role: *role, Listen: *listen, DataDir: *dataDir, EngineURL: *engineURL, FFmpeg: *ffmpeg, DevReload: *devReload, XMPPCAFile: *xmppCA})
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
