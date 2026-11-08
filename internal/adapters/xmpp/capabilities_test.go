package xmpp

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/disco/info"
)

func TestServiceDiscoveryRejectsNodesAndUnrelatedAddresses(t *testing.T) {
	for _, tc := range []struct {
		address, node string
		ok            bool
	}{
		{"upload.example.org", "", true}, {"example.org", "", true}, {"upload.example.org", "node", false},
		{"upload.example.org/other", "", false}, {"someone@example.org", "", false}, {"example.org.attacker.test", "", false}, {"foreign.test", "", false},
	} {
		got, ok := serviceAddress("example.org", tc.address, tc.node)
		if ok != tc.ok || (ok && got != tc.address) {
			t.Fatal(tc, got, ok)
		}
	}
}
func TestBoundedDiscoveryDoesNotInventMissingFeatureSupport(t *testing.T) {
	raw := disco.Info{}
	for i := range 270 {
		raw.Features = append(raw.Features, info.Feature{Var: fmt.Sprintf("urn:test:%03d", i)})
	}
	raw.Features = append(raw.Features, info.Feature{Var: mamNS})
	service := discovered("account", "alice@example.org", raw, nil)
	if !service.Truncated || len(service.Features) != 256 {
		t.Fatal("unbounded evidence", len(service.Features))
	}
	d := domain.CapabilityDiscovery{State: "partial", Services: []domain.DiscoveredService{service}}
	if got := featureEvidence(d, "account", mamNS); got != "unknown" {
		t.Fatal("truncated omission treated as unsupported", got)
	}
	if got := serviceEvidence(d, "urn:xmpp:http:upload:0"); got != "unknown" {
		t.Fatal("partial inventory invented absence", got)
	}
	known := discovered("account", "alice@example.org", disco.Info{}, nil)
	if got := featureEvidence(domain.CapabilityDiscovery{Services: []domain.DiscoveredService{known}}, "account", mamNS); got != "not_advertised" {
		t.Fatal(got)
	}
	// Client protocols do not require a server feature advertisement.
	manager := New(t.TempDir(), nil)
	account := domain.Account{ID: "account", Address: "alice@example.org"}
	evidence, err := manager.CapabilityEvidence(t.Context(), account)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range evidence.Features {
		if row.ID == "send_replies" && (row.ProtocolSupport != "supported" || row.ServerSupport != "not_required") {
			t.Fatal("legacy-compatible replies were server-gated", row)
		}
		if row.ID == "reactions" && row.ServerSupport != "scope_dependent" {
			t.Fatal(row)
		}
	}
}
func TestRoomHistoryEvidenceRemainsScopedAndMixed(t *testing.T) {
	d := domain.CapabilityDiscovery{State: "complete", Services: []domain.DiscoveredService{
		{Scope: "account", State: "known", Features: []string{mamNS}},
	}}
	if got := roomHistoryEvidence(d); got != "scope_dependent" {
		t.Fatal("account MAM implied room support", got)
	}
	d.Services = append(d.Services, domain.DiscoveredService{Scope: "room", State: "known", Features: []string{mamNS}}, domain.DiscoveredService{Scope: "room", State: "failed"})
	if got := roomHistoryEvidence(d); got != "partial" {
		t.Fatal("one room proved all rooms", got)
	}
}
func TestDiscoveryCacheRejectsPreviousSessionAndCopiesSnapshots(t *testing.T) {
	current, previous := new(mx.Session), new(mx.Session)
	e := &entry{session: current, state: domain.Connection{State: "connected"}, discovery: domain.CapabilityDiscovery{State: "pending"}}
	recordDiscovery(e, previous, domain.DiscoveredService{Address: "stale", State: "known"})
	if len(e.discovery.Services) != 0 {
		t.Fatal("stale session evidence leaked")
	}
	service := domain.DiscoveredService{Address: "example.org", Scope: "server", State: "known", Features: []string{"one"}, Identities: []domain.ServiceIdentity{{Name: "test"}}}
	recordDiscovery(e, current, service)
	e.discovery.State = "complete"
	manager := New(t.TempDir(), nil)
	a := domain.Account{ID: "account"}
	manager.entries[a.ID] = e
	snap, err := manager.CapabilityEvidence(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	snap.Discovery.Services[0].Features[0] = "changed"
	snap.Discovery.Services[0].Identities[0].Name = "changed"
	again, _ := manager.CapabilityEvidence(t.Context(), a)
	if again.Discovery.Services[0].Features[0] != "one" || again.Discovery.Services[0].Identities[0].Name != "test" {
		t.Fatal("snapshot aliases mutable cache")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			recordDiscovery(e, current, service)
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			_, _ = manager.CapabilityEvidence(context.Background(), a)
		}
	}()
	wg.Wait()
	e.mu.Lock()
	e.state.State = "error"
	e.mu.Unlock()
	recordDiscovery(e, current, domain.DiscoveredService{Address: "stale-after-disconnect", State: "known"})
	off, _ := manager.CapabilityEvidence(t.Context(), a)
	if off.Discovery.State != "not_connected" || len(off.Discovery.Services) != 0 || !off.Discovery.CheckedAt.IsZero() {
		t.Fatal("disconnected cached evidence exposed")
	}
	if again.Discovery.CheckedAt.After(time.Now()) {
		t.Fatal("non-observed timestamp")
	}
}
