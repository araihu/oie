package omemo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"go.mau.fi/libsignal/protocol"
)

// Pending holds only ciphertext until the user has compared the sender's key.
type Pending struct {
	Peer, StanzaID, Digest string
	At                     time.Time
	Envelope               Envelope
	Bytes                  int
}

func (d *Device) Hold(peer, id string, env Envelope, at time.Time) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 || len(id) > 4096 || len(env.Header.Keys) > 256 || env.Header.From == 0 {
		return ErrInvalid
	}
	digest := sha256.Sum256(raw)
	key := hex.EncodeToString(digest[:])
	return d.update(func(s *state) error {
		addressed := slices.ContainsFunc(env.Header.Keys, func(k WrappedKey) bool { return k.To == s.DeviceID })
		if !addressed {
			return ErrInvalid
		}
		size := len(raw)
		for _, p := range s.Pending {
			if p.Peer == peer && p.Digest == key {
				return nil
			}
			size += p.Bytes
		}
		if len(s.Pending) >= 128 || size > 2<<20 {
			return ErrInvalid
		}
		s.Pending = append(s.Pending, Pending{Peer: peer, StanzaID: id, At: at, Envelope: env, Digest: key, Bytes: len(raw)})
		return nil
	})
}

func (d *Device) Ready() ([]Pending, error) {
	s, err := d.read()
	if err != nil {
		return nil, err
	}
	var ready []Pending
	for _, p := range s.Pending {
		addr := protocol.NewSignalAddress(p.Peer, p.Envelope.Header.From)
		if s.Verified[addr.String()] != "" {
			ready = append(ready, p)
		}
	}
	return ready, nil
}

func (d *Device) Receive(ctx context.Context, p Pending, accept func(string) error) error {
	return d.decryptTo(ctx, p.Peer, p.Envelope, p.Digest, accept)
}
func discardPending(s *state, peer, digest string) {
	if digest != "" {
		s.Pending = slices.DeleteFunc(s.Pending, func(p Pending) bool { return p.Peer == peer && p.Digest == digest })
	}
}

func (d *Device) Discard(p Pending) error {
	return d.update(func(s *state) error { discardPending(s, p.Peer, p.Digest); return nil })
}
