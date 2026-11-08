package xmpp_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/sasl"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func seedSession(t *testing.T, ctx context.Context, name, password string, roots *x509.CertPool) (*mx.Session, net.Conn) {
	t.Helper()
	j, _ := jid.Parse(name)
	j, _ = j.WithResource("history-seed-" + string(id()))
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", xmppEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	s, err := mx.NewClientSession(ctx, j, raw, mx.StartTLS(&tls.Config{ServerName: "localhost", RootCAs: roots, MinVersion: tls.VersionTLS12}), mx.SASL("", password, sasl.ScramSha256, sasl.ScramSha1, sasl.Plain), mx.BindResource())
	if err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	return s, raw
}
func TestExistingHistoryAndSavedRoomRecovery(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	alice, aliceRaw := seedSession(t, ctx, "alice@localhost", "alice-local-only", roots)
	defer func() { _ = aliceRaw.Close() }()
	bob, bobRaw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = bobRaw.Close() }()
	body := "History before Oie registration " + string(id())
	received := make(chan bool, 1)
	go func() {
		_ = alice.Serve(mx.HandlerFunc(func(xmlstream.TokenReadEncoder, *xml.StartElement) error { return nil }))
	}()
	go func() {
		_ = bob.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			if start.Name.Local == "message" {
				var m struct {
					Body string `xml:"body"`
				}
				if xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens)).Decode(&m) == nil && m.Body == body {
					select {
					case received <- true:
					default:
					}
				}
			}
			return nil
		}))
	}()
	if err = bob.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
	}
	to, _ := jid.Parse("bob@localhost")
	if err = alice.Encode(ctx, struct {
		stanza.Message
		Body string `xml:"body"`
	}{stanza.Message{To: to, Type: stanza.ChatMessage, ID: string(id())}, body}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("fixture recipient did not receive seed")
	}
	room := "history-" + string(id()) + "@conference.localhost"
	owner, _ := jid.Parse("alice@localhost")
	publish := `<pubsub xmlns="http://jabber.org/protocol/pubsub"><publish node="urn:xmpp:bookmarks:1"><item id="` + room + `"><conference xmlns="urn:xmpp:bookmarks:1" name="Saved archive room"/></item></publish></pubsub>`
	result, err := alice.SendIQ(ctx, stanza.IQ{To: owner, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(publish))))
	if err != nil {
		t.Fatal(err)
	}
	var published stanza.IQ
	err = xml.NewTokenDecoder(result).Decode(&published)
	_ = result.Close()
	if err != nil || published.Type != stanza.ResultIQ {
		t.Fatalf("bookmark fixture failed: %v %v", published.Type, err)
	}
	_ = aliceRaw.Close()
	_ = bobRaw.Close()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, _ := application.New(store, "hosted", func() (domain.ID, error) { return id(), nil }, time.Now)
	manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer func() { manager.Close() }()
	account, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "History fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.ConfigureConnection(ctx, account.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	var conversation domain.Conversation
	var saved domain.Conversation
	wait := func(fn func() bool) {
		t.Helper()
		for ctx.Err() == nil {
			if fn() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("history recovery timed out")
	}
	wait(func() bool {
		h, err := engine.History(ctx, account.Account.ID)
		return err == nil && (h.State == "complete" || h.State == "partial")
	})
	rows, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: account.Account.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rows {
		if c.Address == "bob@localhost" {
			conversation = c
		}
		if c.Address == room {
			saved = c
		}
	}
	if conversation.ID == "" || conversation.UnreadCount != 0 || saved.Kind != "room" || saved.Title != "Saved archive room" {
		t.Fatalf("restored projections: direct=%+v room=%+v", conversation, saved)
	}
	messages, err := engine.Messages(ctx, conversation.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range messages {
		if m.OriginalText == body {
			count++
			if !m.Historical || !m.IsOwn || m.SendState != "submitted" {
				t.Fatal("wrong historical send evidence")
			}
		}
	}
	if count != 1 {
		t.Fatalf("archive seed count=%d", count)
	}
	pending, err := store.PendingMessages(ctx, time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("history generated outbox sends")
	}
	position, err := engine.HistoryCheckpoint(ctx, account.Account.ID, "alice@localhost")
	if err != nil || position == "" {
		t.Fatal("archive checkpoint missing")
	}
	before, _ := engine.History(ctx, account.Account.ID)
	manager.Close()
	manager = adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		h, err := engine.History(ctx, account.Account.ID)
		return err == nil && h.UpdatedAt.After(before.UpdatedAt) && (h.State == "complete" || h.State == "partial")
	})
	after, _ := engine.History(ctx, account.Account.ID)
	if after.RetainedMessages != before.RetainedMessages {
		t.Fatalf("reconnect duplicated archive: %d -> %d", before.RetainedMessages, after.RetainedMessages)
	}

	manager.Close()
	if err = engine.CommitHistoryCheckpoint(ctx, account.Account.ID, "alice@localhost", "expired-opaque-position"); err != nil {
		t.Fatal(err)
	}
	manager = adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		h, err := engine.History(ctx, account.Account.ID)
		return err == nil && h.UpdatedAt.After(after.UpdatedAt) && (h.State == "complete" || h.State == "partial")
	})
	recovered, _ := engine.History(ctx, account.Account.ID)
	if recovered.RetainedMessages != before.RetainedMessages {
		t.Fatal("expired provider cursor lost/duplicated retained history")
	}
}
