package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

// The fixture changes only Bob's published development vCard and restores the
// complete original XML afterward. It never contacts any real user's server.
func TestPublishedSenderProfileDevelopmentVCardAndScopedRead(t *testing.T) {
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
	publisher, raw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = raw.Close() }()
	var messages, subscriptions atomic.Int64
	go func() {
		_ = publisher.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			switch start.Name.Local {
			case "message":
				var message struct {
					stanza.Message
					Body string `xml:"jabber:client body"`
				}
				if err := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens)).Decode(&message); err != nil {
					return err
				}
				if message.Type == stanza.ChatMessage || message.Type == stanza.GroupChatMessage || message.Body != "" {
					messages.Add(1)
				}
				return nil
			case "presence":
				var p stanza.Presence
				if err := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens)).Decode(&p); err != nil {
					return err
				}
				if p.Type == stanza.SubscribePresence || p.Type == stanza.UnsubscribePresence {
					subscriptions.Add(1)
				}
				return nil
			}
			var ignored any
			return xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens)).Decode(&ignored)
		}))
	}()
	own := jid.MustParse("bob@localhost")
	original, originalType, err := profileFixtureIQ(ctx, publisher, own, stanza.GetIQ, `<vCard xmlns="vcard-temp"/>`)
	if err != nil {
		t.Fatal("read original fixture profile", err)
	}
	if originalType == stanza.ErrorIQ {
		original = `<vCard xmlns="vcard-temp"/>`
	} else if originalType != stanza.ResultIQ {
		t.Fatal("unexpected original profile response")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, kind, restoreErr := profileFixtureIQ(cleanup, publisher, jid.JID{}, stanza.SetIQ, original)
		if restoreErr != nil || kind != stanza.ResultIQ {
			t.Errorf("restore original development profile: type=%s error=%v", kind, restoreErr)
		}
	}()
	publish := func(payload string) {
		t.Helper()
		reply, kind, err := profileFixtureIQ(ctx, publisher, jid.JID{}, stanza.SetIQ, payload)
		if err != nil || kind != stanza.ResultIQ {
			t.Fatal("publish fixture profile", kind, profileFixtureError(reply), err)
		}
	}
	publish(`<vCard xmlns="vcard-temp"><FN>Bob da Silva</FN><JABBERID>forged@unrelated.invalid</JABBERID></vCard>`)
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
	registered, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Profile fixture reader"})
	if err != nil {
		t.Fatal(err)
	}
	connect := func() {
		t.Helper()
		if _, err := engine.ConfigureConnection(ctx, registered.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool {
			c, err := engine.Connection(ctx, registered.Account.ID)
			return err == nil && c.State == "connected"
		})
	}
	connect()
	body := "local profile fixture " + string(id())
	if err := engine.Receive(ctx, application.Incoming{AccountID: registered.Account.ID, Address: "bob@localhost", Kind: "direct", SenderName: "Bob", OriginalText: body, ProviderID: string(id()), CreatedAt: time.Now(), Historical: true}); err != nil {
		t.Fatal(err)
	}
	conversations, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: registered.Account.ID, Kind: "direct", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var source domain.Message
	for _, conversation := range conversations {
		if conversation.Address != "bob@localhost" {
			continue
		}
		items, err := engine.Messages(ctx, conversation.ID, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range items {
			if message.OriginalText == body {
				source = message
			}
		}
	}
	if source.ID == "" {
		t.Fatal("local fixture source missing")
	}
	verify := func(want string) {
		t.Helper()
		beforeMessages, beforeSubscriptions := messages.Load(), subscriptions.Load()
		for range 2 {
			profile, err := engine.MessageSenderProfile(ctx, source.ID)
			if err != nil || profile.State != "known" || profile.FullName != want || profile.PersonalAddress != "bob@localhost" || profile.AccountID != source.AccountID || profile.ConversationID != source.ConversationID || profile.MessageID != source.ID {
				t.Fatalf("profile scope or published identity mismatch: state=%s address=%s name=%q err=%v", profile.State, profile.PersonalAddress, profile.FullName, err)
			}
		}
		retained, err := engine.Message(ctx, source.ID)
		if err != nil || retained.RevisionID != source.RevisionID || retained.OriginalText != source.OriginalText || retained.SendState != source.SendState {
			t.Fatal("profile read mutated retained source", err)
		}
		if messages.Load() != beforeMessages || subscriptions.Load() != beforeSubscriptions {
			t.Fatal("profile read emitted a message or presence subscription")
		}
	}
	verify("Bob da Silva")
	if _, err := engine.Disconnect(ctx, registered.Account.ID); err != nil {
		t.Fatal(err)
	}
	publish(`<vCard xmlns="vcard-temp"><FN> </FN><N><GIVEN>Roberto</GIVEN><FAMILY>Silva</FAMILY></N><JABBERID>forged@unrelated.invalid</JABBERID></vCard>`)
	connect()
	verify("Roberto Silva")
	if _, err := engine.MessageSenderProfile(ctx, id()); err == nil {
		t.Fatal("unknown message gained a profile")
	}
	t.Log("actual Prosody vCard FN/N, session refresh, retained message scope and authoritative address verified; original profile restored on return")
}

// Capture complete vCard XML, including unknown fields, so the fixture can
// restore the original publication instead of retaining only the tested fields.
func profileFixtureIQ(ctx context.Context, s *mx.Session, to jid.JID, kind stanza.IQType, payload string) (string, stanza.IQType, error) {
	response, err := s.SendIQ(ctx, stanza.IQ{To: to, Type: kind}.Wrap(xml.NewDecoder(strings.NewReader(payload))))
	if err != nil {
		return "", "", err
	}
	defer func() { _ = response.Close() }()
	var out strings.Builder
	encoder := xml.NewEncoder(&out)
	for {
		token, err := response.Token()
		if err != nil && err != io.EOF {
			return "", "", err
		}
		if token == nil {
			if err == io.EOF {
				break
			}
			continue
		}
		if out.Len() > 2<<20 {
			return "", "", fmt.Errorf("fixture profile exceeds response limit")
		}
		if encodeErr := encoder.EncodeToken(token); encodeErr != nil {
			return "", "", encodeErr
		}
		if err == io.EOF {
			break
		}
	}
	if err := encoder.Flush(); err != nil {
		return "", "", err
	}
	var iq struct {
		stanza.IQ
		Inner string `xml:",innerxml"`
	}
	if err := xml.Unmarshal([]byte(out.String()), &iq); err != nil {
		return "", "", err
	}
	return iq.Inner, iq.Type, nil
}

func profileFixtureError(payload string) string {
	decoder := xml.NewDecoder(strings.NewReader("<root>" + payload + "</root>"))
	for {
		token, err := decoder.Token()
		if err != nil {
			return "unknown"
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Space == "urn:ietf:params:xml:ns:xmpp-stanzas" && start.Name.Local != "text" {
			return start.Name.Local
		}
	}
}
