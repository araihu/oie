package omemo

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"go.mau.fi/libsignal/protocol"
)

var ErrChanged = errors.New("device identity changed")

type PublicDevice struct {
	ID               uint32
	Fingerprint      string
	Verified, Active bool
}

func (d *Device) DevicesFor(peer string) ([]PublicDevice, bool, error) {
	s, err := d.read()
	if err != nil {
		return nil, false, err
	}
	devices := make(map[uint32]PublicDevice)
	for id, fp := range s.Observed[peer] {
		addr := protocol.NewSignalAddress(peer, id)
		devices[id] = PublicDevice{id, fp, s.Verified[addr.String()] == fp, true}
	}
	for addr, fp := range s.Verified {
		suffix, ok := strings.CutPrefix(addr, peer+":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(suffix, 10, 31)
		if err != nil {
			continue
		}
		if _, ok := devices[uint32(n)]; !ok {
			devices[uint32(n)] = PublicDevice{uint32(n), fp, true, false}
		}
	}
	out := make([]PublicDevice, 0, len(devices))
	for _, d := range devices {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b PublicDevice) int { return int(a.ID) - int(b.ID) })
	return out, !s.PlainPeers[peer], nil
}
func (d *Device) RequiresEncryption(peer string) (bool, error) {
	s, err := d.read()
	if err != nil {
		return false, err
	}
	return !s.PlainPeers[peer], nil
}

func (d *Device) PlaintextPeers() (map[string]bool, error) {
	s, err := d.read()
	if err != nil {
		return nil, err
	}
	return s.PlainPeers, nil
}

// WithPlaintextPeer keeps the preference stable until stored archive data
// for the explicitly opted-in peer has been accepted.
func (d *Device) WithPlaintextPeer(ctx context.Context, peer string, accept func() error) (bool, error) {
	lock := flock.New(d.path + ".lock")
	locked, err := lock.TryRLockContext(ctx, 10*time.Millisecond)
	if err != nil || !locked {
		return false, err
	}
	defer func() { _ = lock.Close() }()
	s, err := d.read()
	if err != nil || !s.PlainPeers[peer] {
		return false, err
	}
	return true, accept()
}

func (d *Device) SetEncryption(peer string, enabled bool) error {
	return d.update(func(s *state) error {
		if enabled {
			delete(s.PlainPeers, peer)
		} else {
			if s.PlainPeers == nil {
				s.PlainPeers = map[string]bool{}
			}
			s.PlainPeers[peer] = true
		}
		return nil
	})
}
func (d *Device) ConfirmObserved(peer string, id uint32, fp string, trust bool) error {
	return d.update(func(s *state) error {
		addr := protocol.NewSignalAddress(peer, id).String()
		if fp == "" || (s.Observed[peer][id] != fp && (trust || s.Verified[addr] != fp)) {
			return ErrChanged
		}
		if !trust || s.Verified[addr] != fp {
			delete(s.Sessions, addr)
		}
		if trust {
			s.Verified[addr] = fp
		} else {
			delete(s.Verified, addr)
		}
		return nil
	})
}

// RepairSession lets a verified peer negotiate fresh session keys on the next send.
func (d *Device) RepairSession(peer string, id uint32, fp string) error {
	return d.update(func(s *state) error {
		addr := protocol.NewSignalAddress(peer, id).String()
		if fp == "" || s.Verified[addr] != fp || s.Observed[peer][id] != fp {
			return ErrUnverified
		}
		delete(s.Sessions, addr)
		return nil
	})
}

func (d *Device) RoomEncryption(room string) (bool, error) {
	s, err := d.read()
	if err != nil {
		return false, err
	}
	return s.EncryptedRooms[room], nil
}

func (d *Device) SetRoomEncryption(room string, enabled bool) error {
	return d.update(func(s *state) error {
		if !enabled {
			delete(s.EncryptedRooms, room)
			if room != "" {
				s.Pending = slices.DeleteFunc(s.Pending, func(message Pending) bool { return message.Room == room })
			}
			return nil
		}
		if s.EncryptedRooms == nil {
			s.EncryptedRooms = map[string]bool{}
		}
		s.EncryptedRooms[room] = true
		return nil
	})
}

// WithVerifiedTargets keeps device verification and the published recipient
// snapshot stable while an already encrypted message is committed to the wire.
// The shared file lock also covers trust changes made by the operator CLI.
func (d *Device) WithVerifiedTargets(ctx context.Context, own string, peers []string, targets []Target, write func()) error {
	lock := flock.New(d.path + ".lock")
	locked, err := lock.TryRLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return ErrUnverified
	}
	defer func() { _ = lock.Close() }()

	s, err := d.read()
	if err != nil {
		return err
	}
	expected := make(map[string]map[uint32]string, len(peers))
	for _, peer := range peers {
		expected[peer] = make(map[uint32]string)
	}
	seen := make(map[uint32]bool, len(targets))
	for _, t := range targets {
		if t.DeviceID == 0 || seen[t.DeviceID] {
			return ErrInvalid
		}
		seen[t.DeviceID] = true
		fp, err := t.Bundle.Fingerprint()
		devices, ok := expected[t.JID]
		if err != nil || !ok || s.Verified[protocol.NewSignalAddress(t.JID, t.DeviceID).String()] != fp {
			return ErrUnverified
		}
		devices[t.DeviceID] = fp
	}
	for peer, devices := range expected {
		observed := s.Observed[peer]
		count := 0
		for id, fp := range observed {
			if peer == own && id == s.DeviceID {
				continue
			}
			if fp == "" || devices[id] != fp {
				return ErrUnverified
			}
			count++
		}
		if count != len(devices) {
			return ErrUnverified
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if write != nil {
		write()
	}
	return nil
}
