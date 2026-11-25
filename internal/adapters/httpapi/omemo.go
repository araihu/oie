package httpapi

import (
	"context"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetOmemoDevices(ctx context.Context, r engineapi.GetOmemoDevicesRequestObject) (engineapi.GetOmemoDevicesResponseObject, error) {
	var peer string
	if r.Params.Peer != nil {
		peer = *r.Params.Peer
	}
	state, err := s.engine.InspectOMEMO(ctx, domain.ID(r.AccountId), peer)
	if err != nil {
		return nil, err
	}
	out := engineapi.OmemoDevices{Address: state.Address, Encrypted: state.Encrypted, Devices: []engineapi.OmemoDevice{}}
	for _, d := range state.Devices {
		out.Devices = append(out.Devices, engineapi.OmemoDevice{Id: int(d.ID), Fingerprint: d.Fingerprint, Own: d.Own, Verified: d.Verified, Active: d.Active})
	}
	return engineapi.GetOmemoDevices200JSONResponse(out), nil
}
func (s *Server) SetOmemoDecision(ctx context.Context, r engineapi.SetOmemoDecisionRequestObject) (engineapi.SetOmemoDecisionResponseObject, error) {
	if r.Body == nil {
		return nil, application.ErrInvalid
	}
	var peer string
	if r.Params.Peer != nil {
		peer = *r.Params.Peer
	}
	choice := application.OmemoDecision{Action: string(r.Body.Action)}
	if r.Body.Fingerprint != nil {
		choice.Fingerprint = *r.Body.Fingerprint
	}
	if r.Body.Device != nil {
		choice.Device = uint32(*r.Body.Device)
	}
	if err := s.engine.DecideOMEMO(ctx, domain.ID(r.AccountId), peer, choice); err != nil {
		return nil, err
	}
	return engineapi.SetOmemoDecision204Response{}, nil
}
