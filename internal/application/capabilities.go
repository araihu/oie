package application

import (
	"context"
	"github.com/araihu/oie/internal/domain"
)

// CapabilityProvider reads cached adapter evidence. It must not issue network
// queries or infer implementation support from server advertisements.
type CapabilityProvider interface {
	CapabilityEvidence(context.Context, domain.Account) (domain.CapabilityEvidence, error)
}

func implementedFeatures() []domain.CapabilityFeature {
	rows := []domain.CapabilityFeature{}
	for _, v := range []struct{ id, app, scope string }{
		{"direct_text", "supported", "account"}, {"account_history", "supported", "account"},
		{"saved_rooms", "supported", "account"}, {"room_history", "supported", "room"},
		{"room_discovery", "unsupported", "service"}, {"room_messaging", "partial", "room"},
		{"file_upload", "unsupported", "service"}, {"image_previews", "supported", "local"},
		{"link_previews", "supported", "local"}, {"received_replies", "supported", "peer"},
		{"send_replies", "supported", "peer"}, {"mentions", "supported", "room"}, {"edit_messages", "partial", "peer"},
		{"retract_messages", "unsupported", "peer"}, {"reactions", "unsupported", "peer"},
		{"read_receipts", "unsupported", "peer"}, {"typing", "unsupported", "peer"},
		{"presence", "partial", "peer"}, {"profile_images", "partial", "peer"},
		{"formatting", "partial", "peer"}, {"e2ee", "unsupported", "peer"},
		{"message_stars", "supported", "local"}, {"schedules", "unsupported", "local"},
		{"sync", "unsupported", "local"},
	} {
		row := domain.CapabilityFeature{ID: v.id, AppSupport: v.app, Scope: v.scope, ProtocolSupport: "unknown", ServerSupport: "unknown"}
		if v.scope == "local" {
			row.ProtocolSupport = "not_required"
			row.ServerSupport = "not_required"
		}
		rows = append(rows, row)
	}
	return rows
}
func (e *Engine) AccountCapabilities(ctx context.Context, id domain.ID) (domain.Capabilities, error) {
	a, err := e.Account(ctx, id)
	if err != nil {
		return domain.Capabilities{}, err
	}
	connection, err := e.Connection(ctx, id)
	if err != nil {
		return domain.Capabilities{}, err
	}
	result := domain.Capabilities{AccountID: id, State: "unknown", Reason: "not_connected", CapabilityEvidence: domain.CapabilityEvidence{Features: implementedFeatures(), Discovery: domain.CapabilityDiscovery{State: "not_connected", Services: []domain.DiscoveredService{}}}}
	if connection.State == "connected" {
		result.State = "known"
		result.Reason = "adapter_connected"
		result.DirectText = true
		result.Discovery.State = "pending"
	}
	if provider, ok := e.protocols.(CapabilityProvider); ok {
		evidence, err := provider.CapabilityEvidence(ctx, a)
		if err != nil {
			return result, err
		}
		// A disconnect observed by the adapter supersedes an earlier connection read.
		if evidence.Discovery.State == "not_connected" {
			result.State = "unknown"
			result.Reason = "not_connected"
			result.DirectText = false
			result.Discovery = domain.CapabilityDiscovery{State: "not_connected", Services: []domain.DiscoveredService{}}
		}
		// Disconnected results never expose stale positive server observations.
		if result.DirectText {
			result.Discovery = evidence.Discovery
		}
		for i := range result.Features {
			for _, observed := range evidence.Features {
				if observed.ID == result.Features[i].ID {
					result.Features[i].ProtocolSupport = observed.ProtocolSupport
					if result.DirectText || observed.ServerSupport == "not_required" || observed.ServerSupport == "scope_dependent" {
						result.Features[i].ServerSupport = observed.ServerSupport
					}
				}
			}
		}
	}
	return result, nil
}
