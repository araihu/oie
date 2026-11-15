package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/araihu/oie/cli/internal/cli"
	"github.com/araihu/oie/cli/internal/cliruntime"
	"github.com/spf13/cobra"
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
		root.PersistentPreRunE = requireHTTPSForKey
		err = root.ExecuteContext(ctx)
	}
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, cliruntime.Diagnostic("oiectl", localProblemDocumentation, root, os.Args[1:], err))
	return cliruntime.ExitCode(err)
}

func requireHTTPSForKey(cmd *cobra.Command, _ []string) error {
	if cmd.Name() == "help" || (cmd.Parent() != nil && cmd.Parent().Name() == "completion") {
		return nil
	}
	server, err := cmd.Flags().GetString("server")
	if err != nil {
		return err
	}
	origin, err := url.Parse(server)
	if err != nil || origin.Scheme != "http" {
		return nil
	}
	mappings, err := cmd.Flags().GetStringArray("auth-env")
	if err != nil {
		return err
	}
	variable := "OIECTL_TOKEN"
	for _, mapping := range mappings {
		if scheme, name, ok := strings.Cut(mapping, "="); ok && scheme == "hostedBearer" {
			variable = name
		}
	}
	if os.Getenv(variable) != "" {
		return &cliruntime.ExitError{Code: 2, Message: "HTTPS is required when using an Oiê API key"}
	}
	return nil
}
