package omemo

import (
	"errors"
	"slices"
	"strconv"
	"strings"

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
