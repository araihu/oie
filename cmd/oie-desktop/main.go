//go:build !server

// Command oie-desktop runs Oie with an embedded engine in a native Wails window.
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/araihu/oie/internal/adapters/desktop"
	"github.com/araihu/oie/internal/diagnostics"
)

func main() {
	var config desktop.Config
	flags := flag.NewFlagSet("oie-desktop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&config.DataDir, "data-dir", "", "database and media directory (defaults to the user's application data directory)")
	flags.StringVar(&config.EngineURL, "engine-url", "", "optional hosted engine URL")
	flags.StringVar(&config.XMPPCAFile, "xmpp-ca-file", "", "additional trusted CA certificate PEM for XMPP TLS")
	flags.StringVar(&config.Language, "lang", "auto", "desktop menu language: auto, en, or pt-BR")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return
		}
		diagnostics.New("", os.Stderr, diagnostics.Desktop, false).Record(diagnostics.ConfigFailed, 0, 0)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := desktop.Run(ctx, config); err != nil {
		os.Exit(1)
	}
}
