package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/araihu/oie/cli/internal/cli"
	"github.com/araihu/oie/cli/internal/cliruntime"
)

//go:embed release.json
var releaseData []byte

func main() {
	os.Exit(run())
}

func run() int {
	var release struct {
		Version string `json:"version"`
		SpecTag string `json:"specTag"`
	}
	if err := json.Unmarshal(releaseData, &release); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	root, err := cli.New()
	if err == nil {
		version := "devel (OpenAPI " + root.Version + ")"
		if release.Version != "" {
			version = release.Version + " (spec " + release.SpecTag + ")"
		}
		root.Version = version
		err = root.ExecuteContext(ctx)
	}
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, cliruntime.Diagnostic("oiectl", localProblemDocumentation, root, os.Args[1:], err))
	return cliruntime.ExitCode(err)
}
