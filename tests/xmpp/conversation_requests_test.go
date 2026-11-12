package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"errors"
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

func TestRoomInvitationWireArchiveAndConfirmEntry(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	seed, raw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = raw.Close() }()
	self := make(chan jid.JID, 16)
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			if start.Name.Local != "presence" {
				return nil
			}
			var p stanza.Presence
			decoder := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens))
			if decoder.Decode(&p) == nil && p.Type == "" && p.From.Resourcepart() == "Bob" {
				select {
				case self <- p.From:
				default:
				}
			}
			return nil
		}))
	}()
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
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	account, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Invited account"})
	if err != nil {
		t.Fatal(err)
	}
	owner := account.Account.ID
	if _, err = engine.ConfigureConnection(ctx, owner, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		connection, err := engine.Connection(ctx, owner)
		return err == nil && connection.State == "connected"
	})
	alice, _ := jid.Parse("alice@localhost")
	for _, mode := range []string{"direct", "mediated"} {
		t.Run(mode, func(t *testing.T) {
			room, _ := jid.Parse("invite-" + string(id()) + "@conference.localhost")
			occupant, _ := room.WithResource("Bob")
			if err := seed.Send(ctx, stanza.Presence{To: occupant}.Wrap(xml.NewDecoder(strings.NewReader(`<x xmlns="http://jabber.org/protocol/muc"/>`)))); err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case from := <-self:
					if !from.Equal(occupant) {
						continue
					}
				case <-ctx.Done():
					t.Fatal("fixture owner failed to enter the room")
				}
				break
			}
			password := "invite-secret-" + string(id())
			configuration := `<query xmlns="http://jabber.org/protocol/muc#owner"><x xmlns="jabber:x:data" type="submit"><field var="FORM_TYPE" type="hidden"><value>http://jabber.org/protocol/muc#roomconfig</value></field><field var="muc#roomconfig_roomsecret"><value>` + password + `</value></field></x></query>`
			response, err := seed.SendIQ(ctx, stanza.IQ{To: room, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(configuration))))
			if err != nil {
				t.Fatal(err)
			}
			var configured stanza.IQ
			err = xml.NewTokenDecoder(response).Decode(&configured)
			_ = response.Close()
			if err != nil || configured.Type != stanza.ResultIQ {
				t.Fatal("fixture room setup", configured.Type, err)
			}
			invite := func() {
				t.Helper()
				destination := alice
				payload := `<x xmlns="jabber:x:conference" jid="` + room.String() + `" password="` + password + `"/>`
				if mode == "mediated" {
					destination = room
					payload = `<x xmlns="http://jabber.org/protocol/muc#user"><invite to="alice@localhost"/></x>`
				}
				if err := seed.Send(ctx, stanza.Message{To: destination, Type: stanza.NormalMessage, ID: string(id())}.Wrap(xml.NewDecoder(strings.NewReader(payload)))); err != nil {
					t.Fatal(err)
				}
			}
			invite()
			var chat domain.Conversation
			eventually(t, func() bool {
				rows, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: owner, Kind: "room", Limit: 100})
				if err != nil {
					return false
				}
				for _, c := range rows {
					if c.Address == room.String() && c.Request == domain.RoomInvitation {
						chat = c
						return true
					}
				}
				return false
			})
			state, err := engine.RoomSession(ctx, chat.ID)
			if err != nil || state.Active || state.State == "joined" {
				t.Fatal("receiving an invite joined the room", state, err)
			}
			archivedFlag := true
			archived, err := engine.UpdateConversationPreferences(ctx, id(), chat.ID, application.ConversationPreferencesInput{AccountID: owner, ExpectedRevisionID: chat.RevisionID, Archived: &archivedFlag})
			if err != nil || !archived.Conversation.Archived || archived.Conversation.Request != domain.RoomInvitation {
				t.Fatal("archive did not retain the invitation", archived, err)
			}
			invite()
			if _, err = engine.JoinRoomInvitation(ctx, id(), chat.ID, "Alice", "", chat.RevisionID); !errors.Is(err, application.ErrStateConflict) {
				t.Fatal("stale invitation action may not start a join", err)
			}
			key := id()
			join, err := engine.JoinRoomInvitation(ctx, key, chat.ID, "Alice", "", archived.Conversation.RevisionID)
			if err != nil || !join.Session.Active {
				t.Fatal("join intent was not accepted", join, err)
			}
			eventually(t, func() bool {
				c, readErr := engine.Conversation(ctx, chat.ID)
				s, stateErr := engine.RoomSession(ctx, chat.ID)
				return readErr == nil && stateErr == nil && c.Request == "" && c.Archived && s.State == "joined"
			})
			replay, err := engine.JoinRoomInvitation(ctx, key, chat.ID, "Alice", "", archived.Conversation.RevisionID)
			if err != nil || !replay.Replayed || replay.Session.IntentRevisionID != join.Session.IntentRevisionID {
				t.Fatal("room entry replay changed the intent", replay, err)
			}
		})
	}
}

// Disable background account import so only the peer-specific MAM lookup can
// find the old server record during this test.
type directRequestHost struct {
	application.ProtocolHost
	application.ConversationRequestHost
}

func TestDirectRequestFindsEarlierServerHistoryWithoutLocalHistory(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	bob, bobRaw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = bobRaw.Close() }()
	priorID := string(id())
	received := make(chan struct{}, 1)
	go func() {
		_ = bob.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			if start.Name.Local != "message" {
				return nil
			}
			var msg stanza.Message
			decoder := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens))
			if decoder.Decode(&msg) == nil && msg.ID == priorID {
				select {
				case received <- struct{}{}:
				default:
				}
			}
			return nil
		}))
	}()
	if err := bob.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
	}
	alice, aliceRaw := seedSession(t, ctx, "alice@localhost", "alice-local-only", roots)
	defer func() { _ = aliceRaw.Close() }()
	bobAddress, _ := jid.Parse("bob@localhost")
	prior := `<body>Earlier outgoing message ` + priorID + `</body>`
	if err := alice.Send(ctx, stanza.Message{To: bobAddress, Type: stanza.ChatMessage, ID: priorID}.Wrap(xml.NewDecoder(strings.NewReader(prior)))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("earlier message did not reach Bob")
	}
	_ = aliceRaw.Close()
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
	if err = manager.Start(ctx, directRequestHost{engine, engine}); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	account, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Archive lookup fixture"})
	if err != nil {
		t.Fatal(err)
	}
	owner := account.Account.ID
	if _, err = engine.ConfigureConnection(ctx, owner, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		connection, err := engine.Connection(ctx, owner)
		return err == nil && connection.State == "connected"
	})
	chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: owner, Limit: 100})
	if err != nil || len(chats) != 0 {
		t.Fatal("local fixture already has history", chats, err)
	}
	defer func() {
		if t.Failed() {
			rows, readErr := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: owner, Limit: 100})
			t.Logf("conversation state: %+v (%v)", rows, readErr)
			events, _ := engine.Events(ctx, 0)
			for _, event := range events {
				if event.Conversation != nil && event.Conversation.DirectHistoryCheck != nil {
					t.Logf("archive check started with %+v", *event.Conversation.DirectHistoryCheck)
				}
			}
		}
	}()
	aliceAddress, _ := jid.Parse("alice@localhost")
	body := "New incoming message " + string(id())
	if err = bob.Send(ctx, stanza.Message{To: aliceAddress, Type: stanza.ChatMessage, ID: string(id())}.Wrap(xml.NewDecoder(strings.NewReader(`<body>`+body+`</body>`)))); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: owner, Limit: 100})
		if err != nil || len(chats) != 1 || chats[0].Address != bobAddress.String() || chats[0].Request != "" {
			return false
		}
		messages, err := engine.Messages(ctx, chats[0].ID, "", 10)
		return err == nil && len(messages) == 1 && messages[0].OriginalText == body
	})
}
