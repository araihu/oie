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

type sentCorrectionWire struct {
	stanza.Message
	Body   string `xml:"jabber:client body"`
	Origin struct {
		ID string `xml:"id,attr"`
	} `xml:"urn:xmpp:sid:0 origin-id"`
	Replacements []struct {
		ID string `xml:"id,attr"`
	} `xml:"urn:xmpp:message-correct:0 replace"`
}

func TestOwnSentCorrectionsDirectAndSavedRoom(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 70*time.Second)
	defer cancel()
	seed, raw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = raw.Close() }()
	room, _ := jid.Parse("corrections-" + string(id()) + "@conference.localhost")
	owner, _ := room.WithResource("Fixture Bob")
	joined := make(chan struct{}, 1)
	wire := make(chan sentCorrectionWire, 64)
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			decoder := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens))
			if start.Name.Local == "presence" {
				var p stanza.Presence
				if decoder.Decode(&p) == nil && p.From.Equal(owner) && p.Type == "" {
					select {
					case joined <- struct{}{}:
					default:
					}
				}
			}
			if start.Name.Local == "message" {
				var m sentCorrectionWire
				if decoder.Decode(&m) == nil && m.Body != "" && (m.From.Bare().Equal(room) || m.From.Bare().String() == "alice@localhost") {
					select {
					case wire <- m:
					default:
					}
				}
			}
			return nil
		}))
	}()
	if err = seed.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
	}
	if err = seed.Send(ctx, stanza.Presence{To: owner}.Wrap(xml.NewDecoder(strings.NewReader(`<x xmlns="http://jabber.org/protocol/muc"/>`)))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("fixture owner did not join")
	}
	config := `<query xmlns="http://jabber.org/protocol/muc#owner"><x xmlns="jabber:x:data" type="submit"><field var="FORM_TYPE" type="hidden"><value>http://jabber.org/protocol/muc#roomconfig</value></field><field var="muc#roomconfig_publicroom"><value>1</value></field><field var="muc#roomconfig_membersonly"><value>0</value></field><field var="muc#roomconfig_persistentroom"><value>0</value></field></x></query>`
	response, err := seed.SendIQ(ctx, stanza.IQ{To: room, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(config))))
	if err != nil {
		t.Fatal(err)
	}
	var configured stanza.IQ
	err = xml.NewTokenDecoder(response).Decode(&configured)
	_ = response.Close()
	if err != nil || configured.Type != stanza.ResultIQ {
		t.Fatal("fixture room setup", err)
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
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	registered, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Own correction fixture"})
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
	direct, err := engine.OpenConversation(ctx, id(), registered.Account.ID, "bob@localhost")
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.RestoreConversation(ctx, registered.Account.ID, room.String(), "room", "Own correction fixture"); err != nil {
		t.Fatal(err)
	}
	rooms, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: registered.Account.ID, Kind: "room", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var savedRoom domain.Conversation
	for _, c := range rooms {
		if c.Address == room.String() {
			savedRoom = c
		}
	}
	if savedRoom.ID == "" {
		t.Fatal("saved room absent")
	}
	session, err := engine.RoomSession(ctx, savedRoom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.JoinRoom(ctx, id(), savedRoom.ID, "Oie correction", session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		s, err := engine.RoomSession(ctx, savedRoom.ID)
		return err == nil && s.Active && s.State == "joined"
	})
	awaitWire := func(text string) sentCorrectionWire {
		t.Helper()
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		for {
			select {
			case m := <-wire:
				if m.Body == text {
					return m
				}
			case <-timer.C:
				t.Fatal("expected outgoing correction missing")
				return sentCorrectionWire{}
			}
		}
	}
	for _, conversation := range []domain.Conversation{direct.Conversation, savedRoom} {
		t.Run(conversation.Kind, func(t *testing.T) {
			originalText := "Original own " + conversation.Kind + " café 🧑🏽‍💻 " + string(id())
			accepted, err := engine.SendMessage(ctx, id(), conversation.ID, originalText)
			if err != nil {
				t.Fatal(err)
			}
			originalWire := awaitWire(originalText)
			if originalWire.ID != string(accepted.Message.ID) || len(originalWire.Replacements) != 0 {
				t.Fatal("wrong original wire")
			}
			var target domain.MessageEditTarget
			eventually(t, func() bool {
				target, err = engine.MessageEditTarget(ctx, accepted.Message.ID)
				return err == nil && target.Eligible
			})
			originalCreated := accepted.Message.CreatedAt
			var previous domain.MessageEditTarget
			for n, text := range []string{"First own correction 😀 " + string(id()), "Second own correction <&> " + string(id())} {
				previous = target
				key := id()
				t.Cleanup(func() {
					if t.Failed() {
						command, ce := engine.MessageEdit(ctx, key)
						current, me := engine.Message(ctx, accepted.Message.ID)
						history, he := engine.MessageEditHistory(ctx, accepted.Message.ID, "", 20)
						t.Logf("correction diagnostics command=%+v err=%v current=%+v err=%v history=%+v err=%v", command, ce, current, me, history, he)
					}
				})
				result, err := engine.EditMessage(ctx, key, target.MessageID, target.CurrentRevisionID, text)
				if err != nil {
					t.Fatal(err)
				}
				if result.Command.ID != key || result.Command.MessageID != accepted.Message.ID {
					t.Fatal("command lost original source")
				}
				observed := awaitWire(text)
				if observed.ID != string(key) || observed.Origin.ID != string(key) || len(observed.Replacements) != 1 || observed.Replacements[0].ID != originalWire.ID || observed.Type != originalWire.Type {
					t.Fatal("correction did not target original client stanza", observed)
				}
				eventually(t, func() bool {
					command, err := engine.MessageEdit(ctx, key)
					if err != nil || command.State != "submitted" {
						return false
					}
					current, err := engine.Message(ctx, accepted.Message.ID)
					return err == nil && current.OriginalText == text && current.Edited && current.CreatedAt.Equal(originalCreated)
				})
				replay, err := engine.EditMessage(ctx, key, previous.MessageID, previous.CurrentRevisionID, text)
				if err != nil || !replay.Replayed || replay.Command.ID != key {
					t.Fatal("correction operation replay changed", err)
				}
				history, err := engine.MessageEditHistory(ctx, accepted.Message.ID, "", 20)
				if err != nil || len(history.Items) != n+2 {
					t.Fatal("own echo duplicated retained revision", len(history.Items), err)
				}
				messages, err := engine.Messages(ctx, conversation.ID, "", 100)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, m := range messages {
					if m.ID == accepted.Message.ID || m.OriginalText == originalText || m.OriginalText == text {
						count++
					}
				}
				if count != 1 {
					t.Fatal("correction duplicated canonical bubble", count)
				}
				eventually(t, func() bool {
					target, err = engine.MessageEditTarget(ctx, accepted.Message.ID)
					return err == nil && target.Eligible && target.OriginalText == text
				})
			}
			if _, err = engine.EditMessage(ctx, id(), previous.MessageID, previous.CurrentRevisionID, "Stale conflicting edit"); err == nil {
				t.Fatal("stale content revision accepted")
			}
		})
	}
}
