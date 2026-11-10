package httpapi_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	xmppadapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type evidenceProtocol struct {
	*protocolStub
	evidence domain.CapabilityEvidence
}

func (p *evidenceProtocol) CapabilityEvidence(context.Context, domain.Account) (domain.CapabilityEvidence, error) {
	return p.evidence, nil
}
func TestCapabilityMatrixSeparatesImplementationProtocolAndEvidence(t *testing.T) {
	p := &protocolStub{connected: true}
	e, store, server := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	at := time.Now().UTC()
	evidence := &evidenceProtocol{protocolStub: p, evidence: domain.CapabilityEvidence{Discovery: domain.CapabilityDiscovery{State: "partial", CheckedAt: at, Services: []domain.DiscoveredService{{Address: "upload.localhost", Scope: "service", State: "known", Features: []string{"urn:xmpp:http:upload:0"}, Identities: []domain.ServiceIdentity{{Category: "store", Type: "file", Name: "Uploads"}}}}}, Features: []domain.CapabilityFeature{{ID: "file_upload", AppSupport: "supported", ProtocolSupport: "supported", ServerSupport: "supported"}, {ID: "reactions", ProtocolSupport: "supported", ServerSupport: "scope_dependent"}, {ID: "account_history", ProtocolSupport: "supported", ServerSupport: "unknown"}}}}
	e.SetProtocols(evidence)
	_, before, _ := store.Snapshot(t.Context())
	client, _ := engineapi.NewClientWithResponses(server.URL)
	res, err := client.GetAccountCapabilitiesWithResponse(t.Context(), string(c.AccountID))
	if err != nil || res.JSON200 == nil || res.JSON200.Discovery.State != "partial" || res.JSON200.Discovery.CheckedAt == nil || !res.JSON200.Discovery.CheckedAt.Equal(at) || len(res.JSON200.Discovery.Services) != 1 {
		t.Fatal("evidence projection", res, err)
	}
	byID := map[string]engineapi.CapabilityFeature{}
	for _, row := range res.JSON200.Features {
		byID[string(row.Id)] = row
	}
	upload := byID["file_upload"]
	if upload.AppSupport != "unsupported" || upload.ProtocolSupport != "supported" || upload.ServerSupport != "supported" {
		t.Fatal("server support enabled unimplemented operation", upload)
	}
	if reaction := byID["reactions"]; reaction.AppSupport != "partial" || reaction.ProtocolSupport != "supported" || reaction.ServerSupport != "scope_dependent" {
		t.Fatal("reaction implementation and scoped protocol support", reaction)
	}
	if byID["account_history"].ServerSupport != "unknown" || byID["reactions"].ServerSupport != "scope_dependent" || byID["message_stars"].ServerSupport != "not_required" || byID["message_search"].AppSupport != "supported" || byID["message_search"].ServerSupport != "not_required" || byID["message_search"].ProtocolSupport != "not_required" {
		t.Fatal("unknown/client/local scope confused", byID)
	}
	repeat, err := client.GetAccountCapabilitiesWithResponse(t.Context(), string(c.AccountID))
	if err != nil || !reflect.DeepEqual(repeat.JSON200, res.JSON200) {
		t.Fatal("read modified evidence", err)
	}
	_, after, _ := store.Snapshot(t.Context())
	if after != before || p.count != 0 {
		t.Fatal("diagnostic query changed application/provider state")
	}
	missing, err := client.GetAccountCapabilitiesWithResponse(t.Context(), string(id()))
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal("foreign account evidence", err)
	}
	if _, err = e.Disconnect(t.Context(), c.AccountID); err != nil {
		t.Fatal(err)
	}
	offline, err := client.GetAccountCapabilitiesWithResponse(t.Context(), string(c.AccountID))
	if err != nil || offline.JSON200 == nil || offline.JSON200.State != "unknown" || offline.JSON200.DirectText != nil || len(offline.JSON200.Discovery.Services) != 0 || offline.JSON200.Discovery.State != "not_connected" {
		t.Fatal("stale server evidence after disconnect", err)
	}
	for _, row := range offline.JSON200.Features {
		if row.Id == "reactions" && row.AppSupport != "partial" {
			t.Fatal("disconnect changed implemented reaction support", row)
		}
		if row.Id == "file_upload" && row.ServerSupport != "unknown" {
			t.Fatal("offline support claim", row)
		}
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("diagnostics queued protocol work", err)
	}
}
func TestOfflineXMPPStillDefinesProtocolSupport(t *testing.T) {
	e, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	e.SetProtocols(xmppadapter.New(t.TempDir(), nil))
	client, _ := engineapi.NewClientWithResponses(server.URL)
	res, err := client.GetAccountCapabilitiesWithResponse(t.Context(), string(c.AccountID))
	if err != nil || res.JSON200 == nil || res.JSON200.Discovery.State != "not_connected" {
		t.Fatal(err)
	}
	for _, row := range res.JSON200.Features {
		if row.Id == "file_upload" && (row.ProtocolSupport != "supported" || row.ServerSupport != "unknown") {
			t.Fatal("offline protocol treated as unsupported", row)
		}
	}
}
func TestCapabilityDisconnectRaceSuppressesEarlierConnectedRead(t *testing.T) {
	p := &protocolStub{connected: true}
	e, _, _ := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	e.SetProtocols(&evidenceProtocol{protocolStub: p, evidence: domain.CapabilityEvidence{Discovery: domain.CapabilityDiscovery{State: "not_connected"}, Features: []domain.CapabilityFeature{{ID: "file_upload", ProtocolSupport: "supported", ServerSupport: "supported"}}}})
	got, err := e.AccountCapabilities(t.Context(), c.AccountID)
	if err != nil || got.DirectText || got.State != "unknown" || got.Discovery.State != "not_connected" {
		t.Fatal("inconsistent connection snapshot", got, err)
	}
	for _, row := range got.Features {
		if row.ID == "file_upload" && row.ServerSupport != "unknown" {
			t.Fatal("stale race feature")
		}
	}
	if _, err = e.AccountCapabilities(t.Context(), domain.ID("invalid")); err != application.ErrInvalid {
		t.Fatal(err)
	}
}
