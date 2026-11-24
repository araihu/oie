package omemo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"go.mau.fi/libsignal/protocol"
)

var ErrRoomEncryptionDisabled = errors.New("OMEMO is disabled for the room")

// Pending holds only ciphertext until the user has compared the sender's key.
type Pending struct {
	Peer, StanzaID, Digest string
	Room, Nickname         string
	// A re-enqueued envelope gets a new ID even when its digest is unchanged.
	ID string `json:",omitempty"`

	At       time.Time
	Envelope Envelope
	Bytes    int
}

func (d *Device) Hold(peer, id string, env Envelope, at time.Time) error {
	return d.HoldPending(Pending{Peer: peer, StanzaID: id, Envelope: env, At: at})
}

func (d *Device) HoldPending(message Pending) error {
	env := message.Envelope
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 || len(message.StanzaID) > 4096 || len(env.Header.Keys) > 256 || env.Header.From == 0 {
		return ErrInvalid
	}
	if message.Room != "" {
		raw = append([]byte(message.Room+"\x00"), raw...)
	}
	digest := sha256.Sum256(raw)
	key := hex.EncodeToString(digest[:])
	return d.update(func(s *state) error {
		if message.Room != "" && !s.EncryptedRooms[message.Room] {
			return ErrInvalid
		}
		addressed := slices.ContainsFunc(env.Header.Keys, func(k WrappedKey) bool { return k.To == s.DeviceID })
		if !addressed {
			return ErrInvalid
		}
		size := len(raw)
		for _, p := range s.Pending {
			if p.Peer == message.Peer && p.Room == message.Room && p.Digest == key {
				return nil
			}
			size += p.Bytes
		}
		if len(s.Pending) >= 128 || size > 2<<20 {
			return ErrInvalid
		}
		message.Bytes, message.Digest = len(raw), key
		message.ID = rand.Text()
		s.Pending = append(s.Pending, message)
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
	return d.decryptTo(ctx, p, accept)
}

func (p Pending) sameEntry(other Pending) bool {
	return p.Peer == other.Peer && p.Room == other.Room && p.Digest == other.Digest && p.ID == other.ID
}

func discardPending(s *state, message Pending) {
	if message.Digest != "" {
		s.Pending = slices.DeleteFunc(s.Pending, message.sameEntry)
	}
}

func (d *Device) Discard(p Pending) error {
	return d.update(func(s *state) error { discardPending(s, p); return nil })
}
