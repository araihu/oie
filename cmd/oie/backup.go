package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/araihu/oie/internal/adapters/sqlite"
)

func runBackup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: oie backup create --output FILE [--data-dir DIR] [--include-media] or oie backup verify FILE")
	}
	switch args[0] {
	case "create":
		return createBackup(ctx, args[1:], stdout, stderr)
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: oie backup verify FILE")
		}
		summary, err := sqlite.VerifyBackup(ctx, args[1])
		if err != nil {
			return fmt.Errorf("verify backup: %w", err)
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(summary)
	default:
		return fmt.Errorf("unknown backup command %q", args[0])
	}
}

func createBackup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("oie backup create", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", envOrDefault("OIE_DATA_DIR", "./data"), "existing engine data directory")
	output := flags.String("output", "", "path for the new backup ZIP file")
	includeMedia := flags.Bool("include-media", false, "include managed media and staged attachments")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" || *dataDir == "" {
		return errors.New("specify the existing profile and an output file with --output")
	}
	if err := saveBackup(ctx, *dataDir, *output, *includeMedia); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, *output)
	return err
}

func saveBackup(ctx context.Context, dataDir, output string, includeMedia bool) error {
	// Stage in the destination directory, then publish without replacing any
	// existing file. A cancelled export never leaves a half-written backup.
	file, err := os.CreateTemp(filepath.Dir(output), ".oie-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	err = sqlite.WriteProfileBackup(ctx, dataDir, file, includeMedia)
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), output); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(output))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
