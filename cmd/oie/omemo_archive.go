package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
	"golang.org/x/term"
)

func archivePassword(path string) (string, error) {
	if path == "" {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return "", errors.New("supply --passphrase-file or use an interactive terminal")
		}
		_, _ = fmt.Fprint(os.Stderr, "Passphrase: ")
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		_, _ = fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if len(data) == 0 {
			return "", errors.New("empty passphrase")
		}
		return string(data), nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", err
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(data) > 4096 || password == "" {
		return "", errors.New("invalid passphrase file")
	}
	return password, nil
}
func runOMEMOArchive(ctx context.Context, action string, args []string, w io.Writer) (err error) {
	flags := flag.NewFlagSet("oie omemo "+action, flag.ContinueOnError)
	root := flags.String("data-dir", envOrDefault("OIE_DATA_DIR", "./data"), "local profile")
	account := flags.String("account", "", "local account UUID")
	filename := flags.String("file", "", "encrypted archive path")
	passfile := flags.String("passphrase-file", "", "file containing the passphrase (defaults to terminal)")
	fingerprint := flags.String("fingerprint", "", "identity fingerprint confirmed after inspecting the archive")
	if err = flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *filename == "" {
		return errors.New("supply --file with the archive path")
	}
	password, err := archivePassword(*passfile)
	if err != nil {
		return err
	}
	if action == "inspect-key" {
		f, err := os.Open(*filename)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		info, err := omemo.InspectIdentity(f, password)
		if err != nil {
			return err
		}
		return writeBackupJSON(w, info)
	}
	protected, err := omemo.ProfileEnabled(*root)
	if err != nil {
		return err
	}
	if !protected {
		return errors.New("initialize the local OMEMO profile first")
	}
	newID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	switch action {
	case "export-history":
		return publishBackup(ctx, *filename, func(dst io.Writer) error { return sqlite.WriteEncryptedHistory(ctx, *root, dst, password) })
	case "import-history":
		summary, err := sqlite.RestoreEncryptedHistory(ctx, *root, *filename, password, domain.ID(newID.String()))
		if err != nil {
			return err
		}
		return writeBackupJSON(w, summary)
	}
	id, err := domain.ParseID(*account)
	if err != nil {
		return err
	}
	// Authenticate and display the selected identity before touching the destination.
	var source *os.File
	if action == "import-key" {
		source, err = os.Open(*filename)
		if err != nil {
			return err
		}
		defer func() { _ = source.Close() }()
		info, err := omemo.InspectIdentity(source, password)
		if err != nil {
			return err
		}
		if info.Fingerprint != *fingerprint {
			return errors.New("inspect the archive and confirm its fingerprint with --fingerprint")
		}
		if _, err = source.Seek(0, io.SeekStart); err != nil {
			return err
		}
	}
	store, err := sqlite.Open(ctx, *root, domain.ID(newID.String()))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	a, err := store.Account(ctx, id)
	if err != nil {
		return err
	}
	path := filepath.Join(omemo.ProfileDir(*root), string(id), "device.json")
	switch action {
	case "export-key":
		d, err := omemo.Existing(path)
		if err != nil {
			return err
		}
		return publishBackup(ctx, *filename, func(out io.Writer) error { return d.ExportIdentity(out, a.Address, password) })
	case "import-key":
		return omemo.RestoreIdentity(source, password, path, a.Address, *fingerprint)
	default:
		return errors.New("unknown archive operation")
	}
}
