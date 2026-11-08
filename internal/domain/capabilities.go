package domain

import "time"

// Capability reports are diagnostic observations, never operation authorization.
type CapabilityFeature struct {
	ID, AppSupport, ProtocolSupport, ServerSupport, Scope string
}
type ServiceIdentity struct{ Category, Type, Name string }
type DiscoveredService struct {
	Truncated                        bool
	Address, Scope, State, ErrorCode string
	Features                         []string
	Identities                       []ServiceIdentity
}
type CapabilityDiscovery struct {
	State     string
	CheckedAt time.Time
	Services  []DiscoveredService
}
type CapabilityEvidence struct {
	Features  []CapabilityFeature
	Discovery CapabilityDiscovery
}
type Capabilities struct {
	AccountID     ID
	State, Reason string
	DirectText    bool
	CapabilityEvidence
}
