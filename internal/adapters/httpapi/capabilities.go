package httpapi

import (
	"context"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/domain"
)

func (s *Server) GetAccountCapabilities(ctx context.Context, r engineapi.GetAccountCapabilitiesRequestObject) (engineapi.GetAccountCapabilitiesResponseObject, error) {
	report, err := s.engine.AccountCapabilities(ctx, domain.ID(r.AccountId))
	if err != nil {
		return nil, err
	}
	out := engineapi.Capabilities{AccountId: string(report.AccountID), State: engineapi.CapabilitiesState(report.State), Reason: engineapi.CapabilitiesReason(report.Reason), Features: []engineapi.CapabilityFeature{}, Discovery: engineapi.CapabilityDiscovery{State: engineapi.CapabilityDiscoveryState(report.Discovery.State), Services: []engineapi.DiscoveredService{}}}
	if report.DirectText {
		out.DirectText = &report.DirectText
	}
	if !report.Discovery.CheckedAt.IsZero() {
		out.Discovery.CheckedAt = &report.Discovery.CheckedAt
	}
	for _, row := range report.Features {
		out.Features = append(out.Features, engineapi.CapabilityFeature{Id: engineapi.CapabilityFeatureId(row.ID), AppSupport: engineapi.SupportState(row.AppSupport), ProtocolSupport: engineapi.SupportState(row.ProtocolSupport), ServerSupport: engineapi.SupportState(row.ServerSupport), Scope: engineapi.CapabilityFeatureScope(row.Scope)})
	}
	for _, service := range report.Discovery.Services {
		item := engineapi.DiscoveredService{Address: service.Address, Truncated: service.Truncated, Scope: engineapi.DiscoveredServiceScope(service.Scope), State: engineapi.DiscoveredServiceState(service.State), Features: service.Features}
		item.Identities = make([]struct {
			Category string  `json:"category"`
			Name     *string `json:"name,omitempty"`
			Type     string  `json:"type"`
		}, 0, len(service.Identities))
		for _, identity := range service.Identities {
			entry := struct {
				Category string  `json:"category"`
				Name     *string `json:"name,omitempty"`
				Type     string  `json:"type"`
			}{Category: identity.Category, Type: identity.Type}
			if identity.Name != "" {
				entry.Name = &identity.Name
			}
			item.Identities = append(item.Identities, entry)
		}
		if service.ErrorCode != "" {
			code := engineapi.DiscoveredServiceErrorCode(service.ErrorCode)
			item.ErrorCode = &code
		}
		out.Discovery.Services = append(out.Discovery.Services, item)
	}
	return engineapi.GetAccountCapabilities200JSONResponse(out), nil
}
