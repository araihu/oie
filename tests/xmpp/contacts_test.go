package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/roster"
	"mellium.im/xmpp/stanza"
)

func TestDevelopmentContactRosterInitialPushRemovalAndRetainedCache(t *testing.T) {
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
	seed, raw := seedSession(t, ctx, "alice@localhost", "alice-local-only", roots)
	defer func() { _ = raw.Close() }()
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			var iq stanza.IQ
			if err := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens)).Decode(&iq); err != nil {
				return err
			}
			if start.Name.Local == "iq" && iq.Type == stanza.SetIQ {
				_, err := xmlstream.Copy(tokens, iq.Result(nil))
				return err
			}
			return nil
		}))
	}()
	contact := jid.MustParse("roster-test-" + string(id()) + "@localhost")
	if err = roster.Set(ctx, seed, roster.Item{JID: contact, Name: "Initial directory fixture", Group: []string{"Friends"}}); err != nil {
		t.Fatal("seed initial development roster", err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := roster.Delete(cleanup, seed, contact); err != nil {
			t.Errorf("remove disposable roster contact: %v", err)
		}
	}()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	account, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Development roster fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.ConfigureConnection(ctx, account.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	query := domain.ContactQuery{AccountID: account.Account.ID, Search: contact.String(), Limit: 100}
	var initial domain.Contact
	eventually(t, func() bool {
		page, err := engine.Contacts(ctx, query)
		if err != nil || len(page.Items) != 1 || len(page.Sources) != 1 || page.Sources[0].State != "known" {
			return false
		}
		initial = page.Items[0]
		return initial.Address == contact.String() && initial.DisplayName == "Initial directory fixture" && initial.Active && len(initial.Groups) == 1 && initial.Groups[0] == "Friends"
	})
	if initial.ConversationID != "" {
		t.Fatal("roster identity invented a conversation")
	}
	if err = roster.Set(ctx, seed, roster.Item{JID: contact, Name: "Renamed via actual push", Group: []string{"Team", "Friends"}}); err != nil {
		t.Fatal("seed push update", err)
	}
	var renamed domain.Contact
	eventually(t, func() bool {
		page, err := engine.Contacts(ctx, query)
		if err != nil || len(page.Items) != 1 {
			return false
		}
		renamed = page.Items[0]
		return renamed.ID == initial.ID && renamed.RevisionID != initial.RevisionID && renamed.DisplayName == "Renamed via actual push" && len(renamed.Groups) == 2
	})
	if err = roster.Delete(ctx, seed, contact); err != nil {
		t.Fatal("seed push removal", err)
	}
	eventually(t, func() bool {
		page, err := engine.Contacts(ctx, query)
		return err == nil && len(page.Items) == 0 && len(page.Sources) == 1 && page.Sources[0].State == "known"
	})
	// Reappearance reuses the observed identity rather than manufacturing a
	// contact from conversation history or a different account's directory.
	if err = roster.Set(ctx, seed, roster.Item{JID: contact, Name: "Restored directory fixture"}); err != nil {
		t.Fatal(err)
	}
	var restored domain.Contact
	eventually(t, func() bool {
		page, err := engine.Contacts(ctx, query)
		if err != nil || len(page.Items) != 1 {
			return false
		}
		restored = page.Items[0]
		return restored.ID == initial.ID && restored.RevisionID != renamed.RevisionID && restored.DisplayName == "Restored directory fixture" && len(restored.Groups) == 0
	})
	manager.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	offline, err := application.New(reopened, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	page, err := offline.Contacts(ctx, query)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != restored.ID || page.Items[0].RevisionID != restored.RevisionID || page.Items[0].DisplayName != "Restored directory fixture" {
		t.Fatal("restart lost provider directory cache", page, err)
	}
	conversations, err := offline.Conversations(ctx, "", application.ConversationQuery{AccountID: account.Account.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range conversations {
		if conversation.Address == contact.String() {
			t.Fatal("directory refresh created a conversation")
		}
	}
}

func TestDevelopmentContactActionsAndAvailability(t *testing.T) {
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
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err := manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	account, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	owner := account.Account.ID
	if _, err := engine.ConfigureConnection(ctx, owner, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		state, err := engine.Connection(ctx, owner)
		return err == nil && state.State == "connected"
	})
	address := "contact-action-" + string(id()) + "@localhost"
	conversation, err := engine.OpenConversation(ctx, id(), owner, address)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Receive(ctx, application.Incoming{AccountID: owner, Address: address, OriginalText: "Retain after removal", ProviderID: "test-contact-history"}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"add", "add", "remove", "remove"} {
		if err := engine.ChangeContact(ctx, owner, address, action); err != nil {
			t.Fatal(action, err)
		}
		page, err := engine.Contacts(ctx, domain.ContactQuery{AccountID: owner, Search: address, Limit: 20})
		if err != nil || (len(page.Items) == 1) != (action == "add") {
			t.Fatal(action, page, err)
		}
	}
	history, err := engine.Messages(ctx, conversation.Conversation.ID, "", 20)
	if err != nil || len(history) != 1 || history[0].OriginalText != "Retain after removal" {
		t.Fatal(history, err)
	}
	bob, conn := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = conn.Close() }()
	type presence struct {
		stanza.Presence
		Show string `xml:"jabber:client show"`
	}
	observed := make(chan presence, 64)
	go func() {
		_ = bob.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			d := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens))
			switch start.Name.Local {
			case "presence":
				var p presence
				if err := d.Decode(&p); err != nil {
					return err
				}
				if p.From.Bare().String() == "alice@localhost" {
					select {
					case observed <- p:
					default:
					}
				}
			case "iq":
				var iq stanza.IQ
				if err := d.Decode(&iq); err != nil {
					return err
				}
				if iq.Type == stanza.SetIQ {
					_, err := xmlstream.Copy(tokens, iq.Result(nil))
					return err
				}
			}
			return nil
		}))
	}()
	alice := jid.MustParse("alice@localhost")
	awaitPresence := func(kind stanza.PresenceType, show string) {
		t.Helper()
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		for {
			select {
			case p := <-observed:
				if p.Type == kind && p.Show == show {
					return
				}
			case <-timer.C:
				t.Fatalf("did not observe presence %q %q", kind, show)
			}
		}
	}
	if err := bob.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
	}
	// Prosody routes subscription answers to resources that requested a roster.
	directory := roster.Fetch(ctx, bob)
	for directory.Next() {
	}
	if err := errors.Join(directory.Err(), directory.Close()); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"decline", "accept"} {
		if err := bob.Send(ctx, stanza.Presence{To: alice, Type: stanza.SubscribePresence}.Wrap(nil)); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool {
			pending, err := engine.ContactRequests(ctx, owner)
			return err == nil && slices.Contains(pending, "bob@localhost")
		})
		if err := engine.ChangeContact(ctx, owner, "bob@localhost", action); err != nil {
			t.Fatal(action, err)
		}
		response := stanza.SubscribedPresence
		if action == "decline" {
			response = stanza.UnsubscribedPresence
		}
		awaitPresence(response, "")
		if err := engine.ChangeContact(ctx, owner, "bob@localhost", action); !errors.Is(err, application.ErrStateConflict) {
			t.Fatal("answer was replayable", action, err)
		}
	}
	if err := bob.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
	}
	current, err := engine.AccountCommunicationPreferences(ctx, owner)
	if err != nil || current.Presence != "available" {
		t.Fatal(current, err)
	}
	saved, err := engine.SetAccountCommunicationPreferences(ctx, owner, current.RevisionID, "dnd", nil)
	if err != nil || saved.Presence != "dnd" {
		t.Fatal(saved, err)
	}
	awaitPresence(stanza.AvailablePresence, "dnd")
	if _, err := engine.SetAccountCommunicationPreferences(ctx, owner, current.RevisionID, "away", nil); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale presence preference", err)
	}
	if state, err := engine.AccountCommunicationPreferences(ctx, owner); err != nil || state != saved {
		t.Fatal(state, err)
	}
	if err := bob.Send(ctx, stanza.Presence{To: alice, Type: stanza.UnsubscribePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
	}
}
