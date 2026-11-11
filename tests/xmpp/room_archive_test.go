package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
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

func TestRoomArchiveBootstrapAndResume(t *testing.T) {
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
	room := jid.MustParse("bootstrap-" + string(id()) + "@conference.localhost")
	occupant, _ := room.WithResource("Fixture Bob")
	joined, lastMessage := make(chan struct{}, 1), make(chan struct{}, 1)
	const count = 53
	lastBody := "Archive fixture " + strconv.Itoa(count-1)
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			decoder := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens))
			switch start.Name.Local {
			case "presence":
				var p stanza.Presence
				if decoder.Decode(&p) == nil && p.From.Equal(occupant) && p.Type == "" {
					select {
					case joined <- struct{}{}:
					default:
					}
				}
			case "message":
				var m sentReplyWire
				if decoder.Decode(&m) == nil && m.From.Equal(occupant) && m.Body == lastBody {
					select {
					case lastMessage <- struct{}{}:
					default:
					}
				}
			}
			return nil
		}))
	}()
	if err := seed.Send(ctx, stanza.Presence{To: occupant}.Wrap(xml.NewDecoder(strings.NewReader(`<x xmlns="http://jabber.org/protocol/muc"/>`)))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("fixture room owner did not join")
	}
	const configuration = `<query xmlns="http://jabber.org/protocol/muc#owner"><x xmlns="jabber:x:data" type="submit"><field var="FORM_TYPE" type="hidden"><value>http://jabber.org/protocol/muc#roomconfig</value></field><field var="muc#roomconfig_publicroom"><value>1</value></field><field var="muc#roomconfig_membersonly"><value>0</value></field><field var="muc#roomconfig_enablearchiving"><value>1</value></field></x></query>`
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
	for i := range count {
		msg := stanza.Message{To: room, Type: stanza.GroupChatMessage, ID: string(id())}
		if err := seed.Send(ctx, msg.Wrap(xml.NewDecoder(strings.NewReader("<body>Archive fixture "+strconv.Itoa(i)+"</body>")))); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-lastMessage:
	case <-ctx.Done():
		t.Fatal("fixture room messages did not arrive")
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
	start := func() *adapter.Manager {
		manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
		engine.SetProtocols(manager)
		if err := manager.Start(ctx, engine); err != nil {
			t.Fatal(err)
		}
		return manager
	}
	manager := start()
	defer manager.Close()
	account, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Archive fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.RestoreConversation(ctx, account.Account.ID, room.String(), "room", "Archive fixture"); err != nil {
		t.Fatal(err)
	}
	c, err := store.RestoredRoom(ctx, account.Account.ID, room.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SetRoomArchiveMode(ctx, c.ID, "on_demand"); err != nil {
		t.Fatal(err)
	}

	if _, err := engine.ConfigureConnection(ctx, account.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	var first domain.RoomArchive
	eventually(t, func() bool {
		first, err = engine.RoomArchive(ctx, c.ID)
		return err == nil && !first.Boundary.IsZero()
	})
	if first.InitialComplete || first.WindowEnd.Sub(first.WindowStart) != 48*time.Hour {
		t.Fatalf("unexpected first page state: %+v", first)
	}
	manager.Close()
	manager = start()
	defer manager.Close()
	var resumed domain.RoomArchive
	eventually(t, func() bool {
		resumed, err = engine.RoomArchive(ctx, c.ID)
		return err == nil && resumed.InitialComplete
	})
	if resumed.Mode != "on_demand" || !resumed.OlderComplete || !resumed.WindowStart.Equal(first.WindowStart) || !resumed.WindowEnd.Equal(first.WindowEnd) {
		t.Fatalf("unexpected resumed state: %+v", resumed)
	}
	messages, err := engine.Messages(ctx, c.ID, "", 100)
	if err != nil || len(messages) != count {
		t.Fatalf("recovered %d messages, want %d: %v", len(messages), count, err)
	}
	for _, message := range messages {
		if !message.Historical {
			t.Fatal("archive entry was stored as live traffic")
		}
	}
	c, err = engine.Conversation(ctx, c.ID)
	if err != nil || c.UnreadCount != 0 {
		t.Fatal("history changed unread count", c.UnreadCount, err)
	}
	result, err := engine.RequestRoomArchive(ctx, c.ID, "", "search", "Archive fixture", "")
	if err != nil || result.Outcome != "unsupported" {
		t.Fatal("development archive unexpectedly reported text search", result.Outcome, err)
	}
}
