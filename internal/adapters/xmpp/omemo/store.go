// Package omemo handles the legacy OMEMO wire format used by Gajim and Conversations.
package omemo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gofrs/flock"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/libsignal/keys/identity"
	"go.mau.fi/libsignal/logger"
	"go.mau.fi/libsignal/protocol"
	"go.mau.fi/libsignal/serialize"
	"go.mau.fi/libsignal/state/record"
	"go.mau.fi/libsignal/util/keyhelper"
)

var (
	ErrUnverified = errors.New("OMEMO device needs verification")
	ErrInvalid    = errors.New("invalid OMEMO data")
	serializer    = serialize.NewProtoBufSerializer()
)

// libsignal's process-wide default logger can include protocol data. Account
// or message content must never enter the engine's diagnostics.
type silentLogger struct{}

func (silentLogger) Debug(string, string)   {}
func (silentLogger) Info(string, string)    {}
func (silentLogger) Warning(string, string) {}
func (silentLogger) Error(string, string)   {}
func (silentLogger) Configure(string)       {}
func init()                                 { logger.Logger = silentLogger{} }

// One Device belongs to one account in a locked local engine profile. Each
// update commits keys and ratchet advances together, including pre-key removal.
type Device struct {
	mu   sync.Mutex
	path string
}

type state struct {
	EncryptedRooms map[string]bool `json:",omitempty"`
	PlainPeers     map[string]bool `json:",omitempty"`
	Pending        []Pending       `json:",omitempty"`
	Version        int
	DeviceID       uint32
	Secret         []byte
	NextPreKey     uint32
	PreKeys        map[uint32][]byte
	Signed         []byte
	Observed       map[string]map[uint32]string `json:",omitempty"`
	Verified       map[string]string
	Sessions       map[string][]byte
}

func Open(path string) (*Device, error) {
	d := &Device{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if _, err := d.read(); err == nil {
		return d, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	pair, err := keyhelper.GenerateIdentityKeyPair()
	if err != nil {
		return nil, err
	}
	signed, err := keyhelper.GenerateSignedPreKey(pair, 1, serializer.SignedPreKeyRecord)
	if err != nil {
		return nil, err
	}
	id, err := rand.Int(rand.Reader, big.NewInt(0x7fffffff))
	if err != nil {
		return nil, err
	}
	secret := pair.PrivateKey().Serialize()
	s := &state{Version: 1, DeviceID: uint32(id.Int64() + 1), Secret: secret[:], NextPreKey: 1, PreKeys: map[uint32][]byte{}, Signed: signed.Serialize(), Verified: map[string]string{}, Sessions: map[string][]byte{}}
	if err = s.replenish(); err != nil {
		return nil, err
	}
	return d, d.write(s)
}

func (s *state) replenish() error {
	for len(s.PreKeys) < 100 {
		id := s.NextPreKey
		if id == 0 || id >= 0xffffff {
			return ErrInvalid
		}
		pair, err := ecc.GenerateKeyPair()
		if err != nil {
			return err
		}
		s.PreKeys[id] = record.NewPreKey(id, pair, serializer.PreKeyRecord).Serialize()
		s.NextPreKey++
	}
	return nil
}
func (d *Device) read() (*state, error) {
	raw, err := os.ReadFile(d.path)
	if err != nil {
		return nil, err
	}
	var s state
	if json.Unmarshal(raw, &s) != nil || s.Version != 1 || s.DeviceID == 0 || len(s.Secret) != 32 || len(s.Signed) == 0 || s.PreKeys == nil || s.Sessions == nil || s.Verified == nil {
		return nil, ErrInvalid
	}
	return &s, nil
}
func (d *Device) write(s *state) error {
	dir := filepath.Dir(d.path)
	f, err := os.CreateTemp(dir, ".device-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = json.NewEncoder(f).Encode(s); err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), d.path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
func (d *Device) update(fn func(*state) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	lock := flock.New(d.path + ".lock")
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	s, err := d.read()
	if err != nil {
		return err
	}
	if err = fn(s); err != nil {
		return err
	}
	return d.write(s)
}

func fingerprint(pub *identity.Key) string {
	raw := pub.PublicKey().PublicKey()
	return hex.EncodeToString(raw[:])
}

// Verify pins a fingerprint compared over another channel. Replacing or
// distrusting a key also discards the session established with that key.
func (d *Device) Verify(peer string, id uint32, fp string) error {
	raw, err := hex.DecodeString(fp)
	if peer == "" || id == 0 || err != nil || len(raw) != 32 {
		return ErrInvalid
	}
	addr := protocol.NewSignalAddress(peer, id).String()
	return d.update(func(s *state) error {
		normalized := hex.EncodeToString(raw)
		if seen := s.Observed[peer][id]; seen != "" && seen != normalized {
			return ErrUnverified
		}
		if s.Verified[addr] != normalized {
			delete(s.Sessions, addr)
		}
		s.Verified[addr] = normalized
		return nil
	})
}
func (d *Device) Distrust(peer string, id uint32) error {
	addr := protocol.NewSignalAddress(peer, id).String()
	return d.update(func(s *state) error { delete(s.Verified, addr); delete(s.Sessions, addr); return nil })
}
func (s *state) GetIdentityKeyPair() *identity.KeyPair {
	pair := ecc.CreateKeyPair(s.Secret)
	return identity.NewKeyPair(identity.NewKey(pair.PublicKey()), pair.PrivateKey())
}
func (s *state) GetLocalRegistrationID() uint32 { return s.DeviceID }
func (s *state) IsTrustedIdentity(_ context.Context, a *protocol.SignalAddress, key *identity.Key) (bool, error) {
	return s.Verified[a.String()] == fingerprint(key), nil
}
func (s *state) SaveIdentity(ctx context.Context, a *protocol.SignalAddress, key *identity.Key) error {
	if ok, _ := s.IsTrustedIdentity(ctx, a, key); !ok {
		return ErrUnverified
	}
	return nil
}
func (s *state) LoadPreKey(_ context.Context, id uint32) (*record.PreKey, error) {
	raw, ok := s.PreKeys[id]
	if !ok {
		return nil, ErrInvalid
	}
	key, err := serializer.PreKeyRecord.Deserialize(raw)
	if err != nil || key.ID != id || len(key.PrivateKey) != 32 {
		return nil, ErrInvalid
	}
	return record.NewPreKey(id, ecc.CreateKeyPair(key.PrivateKey), serializer.PreKeyRecord), nil
}
func (s *state) ContainsPreKey(_ context.Context, id uint32) (bool, error) {
	_, ok := s.PreKeys[id]
	return ok, nil
}
func (s *state) RemovePreKey(_ context.Context, id uint32) error { delete(s.PreKeys, id); return nil }
func (s *state) StorePreKey(_ context.Context, id uint32, k *record.PreKey) error {
	s.PreKeys[id] = k.Serialize()
	return nil
}
func (s *state) LoadSignedPreKey(_ context.Context, id uint32) (*record.SignedPreKey, error) {
	k, err := s.signedKey()
	if err != nil || k.ID() != id {
		return nil, ErrInvalid
	}
	return k, nil
}
func (s *state) ContainsSignedPreKey(ctx context.Context, id uint32) (bool, error) {
	_, err := s.LoadSignedPreKey(ctx, id)
	return err == nil, nil
}
func (s *state) LoadSignedPreKeys(context.Context) ([]*record.SignedPreKey, error) {
	k, err := s.signedKey()
	if err != nil {
		return nil, err
	}
	return []*record.SignedPreKey{k}, nil
}
func (s *state) StoreSignedPreKey(_ context.Context, id uint32, k *record.SignedPreKey) error {
	if k.ID() != id {
		return ErrInvalid
	}
	s.Signed = k.Serialize()
	return nil
}
func (s *state) RemoveSignedPreKey(context.Context, uint32) error {
	return errors.New("cannot remove current signed pre-key")
}
func (s *state) LoadSession(_ context.Context, a *protocol.SignalAddress) (*record.Session, error) {
	raw, ok := s.Sessions[a.String()]
	if !ok {
		return record.NewSession(serializer.Session, serializer.State), nil
	}
	return record.NewSessionFromBytes(raw, serializer.Session, serializer.State)
}
func (s *state) StoreSession(_ context.Context, a *protocol.SignalAddress, v *record.Session) error {
	s.Sessions[a.String()] = v.Serialize()
	return nil
}
func (s *state) ContainsSession(_ context.Context, a *protocol.SignalAddress) (bool, error) {
	_, ok := s.Sessions[a.String()]
	return ok, nil
}
func (s *state) DeleteSession(_ context.Context, a *protocol.SignalAddress) error {
	delete(s.Sessions, a.String())
	return nil
}
func (s *state) GetSubDeviceSessions(_ context.Context, name string) ([]uint32, error) {
	var ids []uint32
	for addr := range s.Sessions {
		i := strings.LastIndexByte(addr, ':')
		if i < 0 || addr[:i] != name {
			continue
		}
		n, err := strconv.ParseUint(addr[i+1:], 10, 32)
		if err != nil {
			return nil, ErrInvalid
		}
		ids = append(ids, uint32(n))
	}
	slices.Sort(ids)
	return ids, nil
}
func (s *state) DeleteAllSessions(context.Context) error { clear(s.Sessions); return nil }

func (s *state) signedKey() (*record.SignedPreKey, error) {
	key, err := serializer.SignedPreKeyRecord.Deserialize(s.Signed)
	if err != nil || len(key.PrivateKey) != 32 || len(key.Signature) != 64 {
		return nil, ErrInvalid
	}
	return record.NewSignedPreKey(key.ID, key.Timestamp, ecc.CreateKeyPair(key.PrivateKey), [64]byte(key.Signature), serializer.SignedPreKeyRecord), nil
}

// ProfileDir is the durable opt-in for an experimental encrypted local profile.
func ProfileDir(root string) string { return filepath.Join(root, "private", "omemo") }
func ProfileEnabled(root string) (bool, error) {
	info, err := os.Lstat(ProfileDir(root))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, ErrInvalid
	}
	return true, nil
}

// Observe updates the list only from a successful authenticated PEP reply. A
// changed identity invalidates its old approval and ratchet together.
func (d *Device) Observe(peer string, devices map[uint32]string) error {
	s, err := d.read()
	if err != nil {
		return err
	}
	if maps.Equal(s.Observed[peer], devices) {
		return nil
	}
	return d.update(func(s *state) error {
		if s.Observed == nil {
			s.Observed = make(map[string]map[uint32]string)
		}
		for id, fp := range devices {
			addr := protocol.NewSignalAddress(peer, id).String()
			if pin := s.Verified[addr]; pin != "" && pin != fp {
				delete(s.Verified, addr)
				delete(s.Sessions, addr)
			}
		}
		s.Observed[peer] = devices
		return nil
	})
}

func (d *Device) LocalIdentity() (uint32, string, error) {
	s, err := d.read()
	if err != nil {
		return 0, "", err
	}
	return s.DeviceID, fingerprint(s.GetIdentityKeyPair().PublicKey()), nil
}

func Existing(path string) (*Device, error) {
	d := &Device{path: path}
	_, err := d.read()
	return d, err
}

func (d *Device) DeviceList() (map[string]string, map[string]string, error) {
	s, err := d.read()
	if err != nil {
		return nil, nil, err
	}
	observed := map[string]string{}
	for peer, devices := range s.Observed {
		for id, fp := range devices {
			observed[protocol.NewSignalAddress(peer, id).String()] = fp
		}
	}
	return observed, s.Verified, nil
}
