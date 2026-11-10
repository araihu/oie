//go:build !server

// Command oie-desktop runs Oie with an embedded engine in a native Wails window.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/araihu/oie/internal/adapters/desktop"
)

func main() {
	var config desktop.Config
	flag.StringVar(&config.DataDir, "data-dir", "", "database and media directory (defaults to the user's application data directory)")
	flag.StringVar(&config.EngineURL, "engine-url", "", "optional hosted engine URL")
	flag.StringVar(&config.FFmpeg, "ffmpeg", "ffmpeg", "optional FFmpeg executable for video thumbnails")
	flag.StringVar(&config.XMPPCAFile, "xmpp-ca-file", "", "additional trusted CA certificate PEM for XMPP TLS")
	flag.StringVar(&config.Language, "lang", "auto", "desktop menu language: auto, en, or pt-BR")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := desktop.Run(ctx, config); err != nil {
		slog.Error("oie desktop stopped", "error", err)
		os.Exit(1)
	}
}
