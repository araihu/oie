package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type detailsProtocol struct {
	*protocolStub
	info  domain.ConversationInfo
	calls int
}

func (p *detailsProtocol) ConversationInfo(_ context.Context, a domain.Account, c domain.Conversation) (domain.ConversationInfo, error) {
	p.calls++
	if a.ID != c.AccountID {
		panic("cross-account details")
	}
	return p.info, nil
}
func TestConversationDetailsContractKeepsUnknownAndReadOnlyState(t *testing.T) {
	p := &protocolStub{}
	e, store, server := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.RestoreConversation(t.Context(), c.AccountID, "room@conference.localhost", "room", "Saved room"); err != nil {
		t.Fatal(err)
	}
	rows, err := e.Conversations(t.Context(), "", application.ConversationQuery{AccountID: c.AccountID, Kind: "room", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	c = rows[0]
	count := 37
	at := time.Now().UTC()
	adapter := &detailsProtocol{protocolStub: p, info: domain.ConversationInfo{State: "partial", Name: "Room name", Topic: "Topic <plain>", OccupantCount: &count, RoomSession: "not_joined", Features: []string{"members_only"}, CheckedAt: at, Participants: domain.ConversationParticipants{Scope: "occupants", State: "forbidden", Items: []domain.ConversationParticipant{}, CheckedAt: at}}}
	e.SetProtocols(adapter)
	client, _ := engineapi.NewClientWithResponses(server.URL)
	off, err := client.GetConversationDetailsWithResponse(t.Context(), string(c.ID))
	if err != nil || off.JSON200 == nil || off.JSON200.State != "not_connected" || len(off.JSON200.Participants.Items) != 0 || adapter.calls != 0 {
		t.Fatal("offline queried provider", err)
	}
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()
	_, before, _ := store.Snapshot(t.Context())
	res, err := client.GetConversationDetailsWithResponse(t.Context(), string(c.ID))
	if err != nil || res.JSON200 == nil || res.JSON200.State != "partial" || res.JSON200.AccountId != string(c.AccountID) || res.JSON200.ConversationId != string(c.ID) || res.JSON200.Address != c.Address || res.JSON200.Participants.State != "forbidden" || res.JSON200.OccupantCount == nil || *res.JSON200.OccupantCount != 37 || res.JSON200.AccountConnection.State != "connected" {
		t.Fatal("details mapping", res, err)
	}
	// The visible list may be inaccessible even when the server reports occupancy.
	if len(res.JSON200.Participants.Items) != 0 || res.JSON200.Topic == nil || *res.JSON200.Topic != "Topic <plain>" {
		t.Fatal("forbidden list fabricated or metadata lost")
	}
	_, after, _ := store.Snapshot(t.Context())
	if before != after || p.count != 0 {
		t.Fatal("query changed state")
	}
	missing, err := client.GetConversationDetailsWithResponse(t.Context(), string(id()))
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal("missing conversation", err)
	}
	if _, err = e.Disconnect(t.Context(), c.AccountID); err != nil {
		t.Fatal(err)
	}
	disconnected, err := client.GetConversationDetailsWithResponse(t.Context(), string(c.ID))
	if err != nil || disconnected.JSON200 == nil || disconnected.JSON200.Name != nil || disconnected.JSON200.State != "not_connected" {
		t.Fatal("stale metadata after disconnect", err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("details queued send", err)
	}
}
