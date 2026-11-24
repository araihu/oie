package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/domain"
	"github.com/gofrs/flock"
	"mellium.im/xmpp/jid"
)

func runOMEMO(ctx context.Context, args []string, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: oie omemo init|devices|verify|distrust|repair|export-key|inspect-key|import-key|export-history|import-history [options]")
	}
	action := args[0]
	switch action {
	case "export-key", "inspect-key", "import-key", "export-history", "import-history":
		return runOMEMOArchive(ctx, action, args[1:], w)
	}
	flags := flag.NewFlagSet("oie omemo "+action, flag.ContinueOnError)
	root := flags.String("data-dir", envOrDefault("OIE_DATA_DIR", "./data"), "local engine profile")
	account := flags.String("account", "", "Oiê account UUID")
	peer := flags.String("peer", "", "bare XMPP address")
	id := flags.Uint64("device", 0, "OMEMO device ID")
	fingerprint := flags.String("fingerprint", "", "fingerprint compared with the device owner")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if action == "init" {
		if err := os.MkdirAll(*root, 0700); err != nil {
			return err
		}
		lock := flock.New(filepath.Join(*root, "engine.lock"))
		defer func() { _ = lock.Close() }()
		ok, err := lock.TryLock()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("stop the local engine before enabling OMEMO")
		}
		return os.MkdirAll(omemo.ProfileDir(*root), 0700)
	}
	if _, err := domain.ParseID(*account); err != nil {
		return err
	}
	enabled, err := omemo.ProfileEnabled(*root)
	if err != nil {
		return err
	}
	if !enabled {
		return errors.New("initialize an OMEMO profile first")
	}
	path := filepath.Join(omemo.ProfileDir(*root), *account, "device.json")
	if _, err := os.Stat(path); err != nil {
		return errors.New("connect the account in this profile first")
	}
	d, err := omemo.Existing(path)
	if err != nil {
		return err
	}
	if action == "devices" {
		device, fp, err := d.LocalIdentity()
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "local %d %s\n", device, fp); err != nil {
			return err
		}
		observed, pins, err := d.DeviceList()
		if err != nil {
			return err
		}
		lines := make([]string, 0, len(pins))
		for k, v := range observed {
			if pins[k] != v {
				lines = append(lines, "unverified "+k+" "+v)
			}
		}
		for k, v := range pins {
			lines = append(lines, "verified "+k+" "+v)
		}
		slices.Sort(lines)
		for _, line := range lines {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		return nil
	}
	j, err := jid.Parse(*peer)
	if err != nil || j.Localpart() == "" || j.Resourcepart() != "" || *id == 0 || *id > 0x7fffffff {
		return errors.New("provide a bare peer address and a valid device ID")
	}
	switch action {
	case "verify":
		return d.Verify(j.Bare().String(), uint32(*id), strings.Join(strings.Fields(*fingerprint), ""))
	case "repair":
		return d.RepairSession(j.Bare().String(), uint32(*id), strings.Join(strings.Fields(*fingerprint), ""))
	case "distrust":
		return d.Distrust(j.Bare().String(), uint32(*id))
	default:
		return errors.New("unknown OMEMO action")
	}
}
