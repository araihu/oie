package application

import (
	"context"

	"github.com/araihu/oie/internal/domain"
)

type OmemoDevice struct {
	ID                    uint32
	Fingerprint           string
	Verified, Active, Own bool
}
type OmemoDevices struct {
	Address   string
	Encrypted bool
	Devices   []OmemoDevice
}
type OmemoDecision struct {
	Action, Fingerprint string
	Device              uint32
}
type OmemoProtocol interface {
	OMEMOEnabled() bool
	DirectEncryption(domain.Account, string) (string, error)
	InspectOMEMO(context.Context, domain.Account, string) (OmemoDevices, error)
	DecideOMEMO(context.Context, domain.Account, string, OmemoDecision) error
}

func (e *Engine) OMEMOEnabled() bool {
	p, ok := e.protocols.(OmemoProtocol)
	return ok && p.OMEMOEnabled()
}
func (e *Engine) omemoScope(ctx context.Context, id domain.ID, peer string) (OmemoProtocol, domain.Account, string, error) {
	p, ok := e.protocols.(OmemoProtocol)
	if !ok || !p.OMEMOEnabled() {
		return nil, domain.Account{}, "", ErrProtocolUnavailable
	}
	a, err := e.Account(ctx, id)
	if err != nil {
		return nil, a, "", err
	}
	if peer == "" {
		peer = a.Address
	}
	address, err := e.protocols.NormalizeAddress("xmpp", peer)
	if err != nil || address != peer {
		return nil, a, "", ErrInvalid
	}
	return p, a, address, nil
}
func (e *Engine) InspectOMEMO(ctx context.Context, id domain.ID, peer string) (OmemoDevices, error) {
	p, a, peer, err := e.omemoScope(ctx, id, peer)
	if err != nil {
		return OmemoDevices{}, err
	}
	return p.InspectOMEMO(ctx, a, peer)
}
func (e *Engine) DecideOMEMO(ctx context.Context, id domain.ID, peer string, decision OmemoDecision) error {
	p, a, peer, err := e.omemoScope(ctx, id, peer)
	if err != nil {
		return err
	}
	return p.DecideOMEMO(ctx, a, peer, decision)
}
