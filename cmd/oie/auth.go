package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/araihu/oie/internal/adapters/hostauth"
)

const authUsage = "usage: oie auth init|recover|key-create|key-list|key-revoke [options]"

func runAuth(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(authUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	flags := flag.NewFlagSet("oie auth "+args[0], flag.ContinueOnError)
	dataDir := flags.String("data-dir", envOrDefault("OIE_DATA_DIR", "./data"), "engine data directory")
	var action func(hostauth.Store) error
	switch args[0] {
	case "init", "recover":
		issuer := flags.String("issuer", os.Getenv("OIE_OIDC_ISSUER"), "owner's exact OIDC issuer URL")
		subject := flags.String("subject", os.Getenv("OIE_OIDC_SUBJECT"), "owner's exact OIDC subject claim")
		action = func(store hostauth.Store) error {
			owner := hostauth.Owner{Issuer: *issuer, Subject: *subject}
			if args[0] == "init" {
				return store.Initialize(ctx, owner)
			}
			if err := store.Recover(ctx, owner); err != nil {
				return err
			}
			_, err := fmt.Fprintln(out, "Owner configured. Existing sessions and API keys have been revoked.")
			return err
		}
	case "key-create":
		name := flags.String("name", "", "label for this full-access key")
		lifetime := flags.Duration("ttl", hostauth.DefaultKeyLifetime, "key lifetime (for example, 720h for 30 days)")
		action = func(store hostauth.Store) error {
			_, secret, err := store.CreateKey(ctx, *name, *lifetime)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, secret)
			return err
		}
	case "key-list":
		action = func(store hostauth.Store) error {
			keys, err := store.ListKeys()
			if err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(keys)
		}
	case "key-revoke":
		id := flags.String("id", "", "key ID from key-list")
		action = func(store hostauth.Store) error {
			if *id == "" {
				return errors.New("key-revoke requires --id")
			}
			return store.RevokeKey(ctx, *id)
		}
	default:
		return errors.New(authUsage)
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New(authUsage)
	}
	return action(hostauth.NewStore(*dataDir))
}
