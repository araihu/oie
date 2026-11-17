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
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func runBackup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: oie backup {create|verify|restore|pending|discard|resume}")
	}
	switch args[0] {
	case "create":
		return createBackup(ctx, args[1:], stdout, stderr)
	case "restore", "pending", "discard", "resume":
		return runBackupRestore(ctx, args[0], args[1:], stdout, stderr)
	case "verify":
		if len(args) != 2 {
			return errors.New("usage: oie backup verify FILE")
		}
		summary, err := sqlite.VerifyBackup(ctx, args[1])
		if err != nil {
			return fmt.Errorf("verify backup: %w", err)
		}
		return writeBackupJSON(stdout, summary)
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

func writeBackupJSON(dst io.Writer, value any) error {
	encoder := json.NewEncoder(dst)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func runBackupRestore(ctx context.Context, action string, args []string, stdout, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("oie backup "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("data-dir", envOrDefault("OIE_DATA_DIR", "./data"), "destination engine profile")
	var message, revision string
	var confirmed bool
	if action == "discard" || action == "resume" {
		flags.StringVar(&message, "message-id", "", "message UUID shown by backup pending")
		flags.StringVar(&revision, "revision-id", "", "revision UUID reviewed in backup pending")
	}
	if action == "resume" {
		flags.BoolVar(&confirmed, "confirm-not-sent", false, "confirm the original send was never transmitted")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" || (action == "restore" && flags.NArg() != 1) || (action != "restore" && flags.NArg() != 0) {
		return fmt.Errorf("invalid arguments for backup %s", action)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if action == "restore" {
		result, err := sqlite.RestoreProfileBackup(ctx, *dir, flags.Arg(0), domain.ID(id.String()))
		if err != nil {
			return err
		}
		return writeBackupJSON(stdout, result)
	}
	if info, err := os.Stat(filepath.Join(*dir, "oie.sqlite")); err != nil || !info.Mode().IsRegular() {
		return errors.New("the selected data directory has no Oiê database")
	}
	if action == "resume" && !confirmed {
		return errors.New("review the paused send and explicitly confirm with --confirm-not-sent")
	}
	store, err := sqlite.Open(ctx, *dir, domain.ID(id.String()))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if action == "pending" {
		items, err := store.RecoveredSends(ctx)
		if err != nil {
			return err
		}
		return writeBackupJSON(stdout, items)
	}
	messageID, err := domain.ParseID(message)
	if err != nil {
		return fmt.Errorf("message ID: %w", err)
	}
	revisionID, err := domain.ParseID(revision)
	if err != nil {
		return fmt.Errorf("revision ID: %w", err)
	}
	return store.ReviewRecoveredSend(ctx, messageID, revisionID, action == "resume", time.Now().UTC())
}
