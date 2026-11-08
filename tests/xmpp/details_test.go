package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestRoomDetailsObservePublicOccupantsWithoutJoining(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("development Prosody required")
	}
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("fixture CA")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	seed, raw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = raw.Close() }()
	room, _ := jid.Parse("details-" + string(id()) + "@conference.localhost")
	occupant, _ := room.WithResource("Fixture Bob")
	joined := make(chan struct{}, 1)
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			if start.Name.Local == "presence" {
				var p stanza.Presence
				if xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens)).Decode(&p) == nil && p.From.Equal(occupant) && p.Type == "" {
					select {
					case joined <- struct{}{}:
					default:
					}
				}
			}
			return nil
		}))
	}()
	if err = seed.Send(ctx, stanza.Presence{To: occupant}.Wrap(xml.NewDecoder(strings.NewReader(`<x xmlns="http://jabber.org/protocol/muc"/>`)))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("fixture room join failed")
	}
	configuration := `<query xmlns="http://jabber.org/protocol/muc#owner"><x xmlns="jabber:x:data" type="submit"><field var="FORM_TYPE" type="hidden"><value>http://jabber.org/protocol/muc#roomconfig</value></field><field var="muc#roomconfig_roomname"><value>Oie details fixture</value></field><field var="muc#roomconfig_roomdesc"><value>Public test room</value></field><field var="muc#roomconfig_publicroom"><value>1</value></field><field var="muc#roomconfig_membersonly"><value>0</value></field><field var="muc#roomconfig_persistentroom"><value>0</value></field></x></query>`
	response, err := seed.SendIQ(ctx, stanza.IQ{To: room, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(configuration))))
	if err != nil {
		t.Fatal(err)
	}
	var configured stanza.IQ
	err = xml.NewTokenDecoder(response).Decode(&configured)
	_ = response.Close()
	if err != nil || configured.Type != stanza.ResultIQ {
		t.Fatal("fixture room configuration", configured.Type, err)
	}
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "hosted", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	registered, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Details observer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.ConfigureConnection(ctx, registered.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		c, err := engine.Connection(ctx, registered.Account.ID)
		return err == nil && c.State == "connected"
	})
	if err = engine.RestoreConversation(ctx, registered.Account.ID, room.String(), "room", "Saved details fixture"); err != nil {
		t.Fatal(err)
	}
	rows, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: registered.Account.ID, Kind: "room", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var conversation domain.Conversation
	for _, c := range rows {
		if c.Address == room.String() {
			conversation = c
			break
		}
	}
	if conversation.ID == "" {
		t.Fatal("fixture conversation absent")
	}
	var details domain.ConversationDetails
	eventually(t, func() bool {
		var err error
		details, err = engine.ConversationDetails(ctx, conversation.ID)
		return err == nil && details.State != "pending"
	})
	if details.Name != "Oie details fixture" || details.Description != "Public test room" || details.RoomSession != "not_joined" || details.AccountConnection.State != "connected" {
		t.Fatal("room metadata unavailable", details.State, details.Name, details.Description)
	}
	if details.OccupantCount == nil || *details.OccupantCount != 1 {
		t.Fatal("observer joined the room or occupancy was lost", details.OccupantCount)
	}
	// Prosody may withhold occupants from a non-occupant even in a public room.
	// A reported occupied room with a hidden list must stay partial, never empty/offline.
	if len(details.Participants.Items) == 0 && details.Participants.State != "partial" && details.Participants.State != "forbidden" {
		t.Fatal("hidden occupants presented as a complete empty room", details.Participants.State)
	}
	for _, p := range details.Participants.Items {
		if p.ID != occupant.String() || p.Presence.State != "unknown" {
			t.Fatal("unvalidated occupant or inferred presence", p)
		}
	}
	checked := details.CheckedAt
	for range 5 {
		again, err := engine.ConversationDetails(ctx, conversation.ID)
		if err != nil || !again.CheckedAt.Equal(checked) {
			t.Fatal("GET repeated discovery", err)
		}
	}
	pending, err := store.PendingMessages(ctx, time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("details queued messages", err)
	}
	if _, err = engine.Disconnect(ctx, registered.Account.ID); err != nil {
		t.Fatal(err)
	}
	off, err := engine.ConversationDetails(ctx, conversation.ID)
	if err != nil || off.State != "not_connected" || len(off.Participants.Items) != 0 || off.Name != "" {
		t.Fatal("disconnect retained live observations", err)
	}
}
