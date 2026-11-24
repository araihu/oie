package xmpp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (m *Manager) OMEMOEnabled() bool { return m.omemoDir != "" }
func (m *Manager) accountDevice(id domain.ID) (*omemo.Device, error) {
	if !m.OMEMOEnabled() {
		return nil, application.ErrProtocolUnavailable
	}
	return omemo.Existing(filepath.Join(m.omemoDir, string(id), "device.json"))
}
func (m *Manager) DirectEncryption(a domain.Account, peer string) (string, error) {
	if !m.OMEMOEnabled() {
		return "", nil
	}
	d, err := m.accountDevice(a.ID)
	if errors.Is(err, os.ErrNotExist) {
		return "omemo", nil
	}
	if err != nil {
		return "", err
	}
	encrypted, err := d.RequiresEncryption(peer)
	if err != nil {
		return "", err
	}
	if encrypted {
		return "omemo", nil
	}
	return "plain", nil
}
func (m *Manager) InspectOMEMO(ctx context.Context, a domain.Account, peer string) (application.OmemoDevices, error) {
	d, err := m.accountDevice(a.ID)
	if err != nil {
		return application.OmemoDevices{}, application.ErrProtocolUnavailable
	}
	self, fp, err := d.LocalIdentity()
	if err != nil {
		return application.OmemoDevices{}, err
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e != nil {
		e.mu.RLock()
		session := e.session
		e.mu.RUnlock()
		if session != nil {
			var skip uint32
			if peer == a.Address {
				skip = self
			}
			refresh, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, _ = observeDevices(refresh, e, session, peer, skip)
			cancel()
		}
	}
	values, encrypted, err := d.DevicesFor(peer)
	if err != nil {
		return application.OmemoDevices{}, err
	}
	out := application.OmemoDevices{Address: peer, Encrypted: encrypted}
	for _, v := range values {
		out.Devices = append(out.Devices, application.OmemoDevice{ID: v.ID, Fingerprint: v.Fingerprint, Active: v.Active, Verified: v.Verified})
	}
	if peer == a.Address {
		out.Devices = append(out.Devices, application.OmemoDevice{ID: self, Fingerprint: fp, Verified: true, Active: true, Own: true})
		slices.SortFunc(out.Devices, func(a, b application.OmemoDevice) int { return int(a.ID) - int(b.ID) })
	}
	return out, nil
}
func (m *Manager) DecideOMEMO(_ context.Context, a domain.Account, peer string, choice application.OmemoDecision) error {
	d, err := m.accountDevice(a.ID)
	if err != nil {
		return application.ErrProtocolUnavailable
	}
	switch choice.Action {
	case "encrypt", "plaintext":
		if peer == a.Address || choice.Device != 0 || choice.Fingerprint != "" {
			return application.ErrInvalid
		}
		return d.SetEncryption(peer, choice.Action == "encrypt")
	case "verify", "distrust":
		if choice.Device == 0 || len(choice.Fingerprint) != 64 {
			return application.ErrInvalid
		}
		err = d.ConfirmObserved(peer, choice.Device, choice.Fingerprint, choice.Action == "verify")
		if errors.Is(err, omemo.ErrChanged) {
			return application.ErrStateConflict
		}
		return err
	default:
		return application.ErrInvalid
	}
}
