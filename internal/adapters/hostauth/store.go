// Package hostauth manages the credentials for one hosted Oiê engine.
package hostauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/gofrs/flock"
	"github.com/google/uuid"
)

const (
	keyPrefix       = "oie_"
	sessionLifetime = 7 * 24 * time.Hour
)

var (
	ErrUnconfigured      = errors.New("hosted owner is not configured; run oie auth recover")
	errUnauthorized      = errors.New("invalid or expired credential")
	errAlreadyConfigured = errors.New("hosted owner is already configured")
)

type Owner struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

type KeyInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type apiKey struct {
	KeyInfo
	Verifier string `json:"verifier"`
}

type session struct {
	Verifier  string    `json:"verifier"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type state struct {
	Owner      Owner     `json:"owner"`
	Generation string    `json:"generation"`
	Keys       []apiKey  `json:"keys"`
	Sessions   []session `json:"sessions"`
}

// Store keeps host credentials outside the directories cleared by an engine reset.
// Whole-file replacement gives readers a consistent view while host commands run.
type Store struct {
	dir string
}

func NewStore(dataDir string) Store {
	return Store{dir: filepath.Join(dataDir, "host-auth")}
}

func (s Store) read() (state, error) {
	var current state
	dirInfo, err := os.Lstat(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return current, ErrUnconfigured
	}
	if err != nil {
		return current, err
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 {
		return current, errors.New("host-auth must be a private directory (0700)")
	}
	path := filepath.Join(s.dir, "state.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return current, ErrUnconfigured
	}
	if err != nil {
		return current, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return current, errors.New("host-auth/state.json must be a private regular file (0600)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return current, err
	}
	if err := json.Unmarshal(data, &current); err != nil || current.Generation == "" ||
		!validOwner(current.Owner) {
		return state{}, errors.New("invalid hosted authentication state")
	}
	return current, nil
}

func (s Store) update(ctx context.Context, initialize bool, change func(*state) error) (err error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	dirInfo, err := os.Lstat(s.dir)
	if err != nil {
		return err
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("host-auth must be a private directory (0700)")
	}
	lock := flock.New(filepath.Join(s.dir, "state.lock"))
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("could not lock hosted authentication state")
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	current, err := s.read()
	if err != nil && (!initialize || !errors.Is(err, ErrUnconfigured)) {
		return err
	}
	if err := change(&current); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	file, err := os.CreateTemp(s.dir, ".state-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}()
	if err := json.NewEncoder(file).Encode(current); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(s.dir, "state.json")); err != nil {
		return err
	}
	return dir.Sync()
}

func validOwner(owner Owner) bool {
	issuer, err := url.Parse(owner.Issuer)
	return err == nil && issuer.Scheme == "https" && issuer.Hostname() != "" &&
		issuer.User == nil && !issuer.ForceQuery && issuer.RawQuery == "" && !strings.Contains(owner.Issuer, "#") &&
		owner.Subject != "" && len(owner.Subject) <= 255 && strings.TrimSpace(owner.Subject) == owner.Subject &&
		!strings.ContainsFunc(owner.Subject, unicode.IsControl)
}

// Initialize records the first owner. Restarts leave recovered identities intact.
func (s Store) Initialize(ctx context.Context, owner Owner) error {
	err := s.setOwner(ctx, owner, true)
	if errors.Is(err, errAlreadyConfigured) {
		return nil
	}
	return err
}

// Recover sets the owner and invalidates all existing authenticators together.
func (s Store) Recover(ctx context.Context, owner Owner) error {
	return s.setOwner(ctx, owner, false)
}

func (s Store) setOwner(ctx context.Context, owner Owner, firstTimeOnly bool) error {
	if !validOwner(owner) {
		return errors.New("provide an HTTPS issuer and a valid OIDC subject")
	}
	return s.update(ctx, true, func(current *state) error {
		if firstTimeOnly && current.Generation != "" {
			return errAlreadyConfigured
		}
		*current = state{Owner: owner, Generation: newCredential()}
		return nil
	})
}

const DefaultKeyLifetime = 90 * 24 * time.Hour

var ErrInvalidKey = errors.New("provide a key name of up to 100 bytes and a lifetime of at least one second")

func (s Store) CreateKey(ctx context.Context, name string, lifetime time.Duration) (KeyInfo, string, error) {
	return s.createKey(ctx, name, lifetime, "")
}

func (s Store) createKey(ctx context.Context, name string, lifetime time.Duration, ownerSession string) (KeyInfo, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) || lifetime < time.Second {
		return KeyInfo{}, "", ErrInvalidKey
	}
	secret := keyPrefix + newCredential()
	now := time.Now().UTC()
	info := KeyInfo{ID: uuid.NewString(), Name: name, CreatedAt: now, ExpiresAt: now.Add(lifetime)}
	if err := s.update(ctx, false, func(current *state) error {
		if ownerSession != "" && !current.validSession(verifier(ownerSession), time.Now()) {
			return errUnauthorized
		}
		current.Keys = append(current.Keys, apiKey{KeyInfo: info, Verifier: verifier(secret)})
		return nil
	}); err != nil {
		return KeyInfo{}, "", err
	}
	return info, secret, nil
}

func (s Store) ListKeys() ([]KeyInfo, error) {
	current, err := s.read()
	if err != nil {
		return nil, err
	}
	keys := make([]KeyInfo, 0, len(current.Keys))
	for _, entry := range current.Keys {
		keys = append(keys, entry.KeyInfo)
	}
	return keys, nil
}

func (s Store) RevokeKey(ctx context.Context, id string) error {
	return s.update(ctx, false, func(current *state) error {
		index := slices.IndexFunc(current.Keys, func(key apiKey) bool { return key.ID == id })
		if index < 0 {
			return errors.New("API key ID not found")
		}
		current.Keys = slices.Delete(current.Keys, index, index+1)
		return nil
	})
}

func (s Store) createSession(ctx context.Context, generation string) (string, error) {
	secret := newCredential()
	now := time.Now().UTC()
	err := s.update(ctx, false, func(current *state) error {
		if current.Generation != generation {
			return errUnauthorized
		}
		current.Sessions = slices.DeleteFunc(current.Sessions, func(item session) bool { return !now.Before(item.ExpiresAt) })
		const maxSessions = 128
		if len(current.Sessions) >= maxSessions {
			current.Sessions = slices.Delete(current.Sessions, 0, len(current.Sessions)-maxSessions+1)
		}
		current.Sessions = append(current.Sessions, session{Verifier: verifier(secret), ExpiresAt: now.Add(sessionLifetime)})
		return nil
	})
	return secret, err
}

func (s Store) revokeSession(ctx context.Context, secret string) error {
	digest := verifier(secret)
	return s.update(ctx, false, func(current *state) error {
		current.Sessions = slices.DeleteFunc(current.Sessions, func(item session) bool {
			return subtle.ConstantTimeCompare([]byte(item.Verifier), []byte(digest)) == 1
		})
		return nil
	})
}

func (s Store) validCredential(secret string, key bool) (bool, error) {
	raw := secret
	if key {
		var found bool
		raw, found = strings.CutPrefix(secret, keyPrefix)
		if !found {
			return false, nil
		}
	}
	if len(raw) != base64.RawURLEncoding.EncodedLen(32) {
		return false, nil
	}
	if _, err := base64.RawURLEncoding.Strict().DecodeString(raw); err != nil {
		return false, nil
	}
	current, err := s.read()
	if err != nil {
		return false, err
	}
	digest := verifier(secret)
	now := time.Now()
	if key {
		return slices.ContainsFunc(current.Keys, func(item apiKey) bool {
			return subtle.ConstantTimeCompare([]byte(item.Verifier), []byte(digest)) == 1 && now.Before(item.ExpiresAt)
		}), nil
	}
	return current.validSession(digest, now), nil
}

func (s state) validSession(digest string, now time.Time) bool {
	return slices.ContainsFunc(s.Sessions, func(item session) bool {
		return subtle.ConstantTimeCompare([]byte(item.Verifier), []byte(digest)) == 1 && now.Before(item.ExpiresAt)
	})
}

func newCredential() string {
	var entropy [32]byte
	rand.Read(entropy[:])
	return base64.RawURLEncoding.EncodeToString(entropy[:])
}

func verifier(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
