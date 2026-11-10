package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type receivedReactionWire struct {
	stanza.Message
	Body      string `xml:"jabber:client body"`
	Reactions []struct {
		ID    string   `xml:"id,attr"`
		Items []string `xml:"urn:xmpp:reactions:0 reaction"`
	} `xml:"urn:xmpp:reactions:0 reactions"`
}

func TestDirectReactionsWireEngineAndHTTPSetReplaceClear(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 65*time.Second)
	defer cancel()
	seed, raw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = raw.Close() }()
	wire := make(chan receivedReactionWire, 32)
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			if start.Name.Local != "message" {
				return nil
			}
			var msg receivedReactionWire
			decoder := xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), tokens))
			if decoder.Decode(&msg) == nil && msg.From.Bare().String() == "alice@localhost" && len(msg.Reactions) == 1 {
				select {
				case wire <- msg:
				default:
				}
			}
			return nil
		}))
	}()
	if err = seed.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(nil)); err != nil {
		t.Fatal(err)
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
	registered, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Reaction fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.ConfigureConnection(ctx, registered.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		state, err := engine.Connection(ctx, registered.Account.ID)
		return err == nil && state.State == "connected"
	})
	to, _ := jid.Parse("alice@localhost")
	sourceWireID, body := "reaction-source-"+string(id()), "Reaction fixture source "+string(id())
	if err = seed.Send(ctx, stanza.Message{To: to, Type: stanza.ChatMessage, ID: sourceWireID}.Wrap(xml.NewDecoder(strings.NewReader(`<body>`+stanzaText(body)+`</body>`)))); err != nil {
		t.Fatal(err)
	}
	var conversation domain.Conversation
	var source domain.Message
	eventually(t, func() bool {
		chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: registered.Account.ID, Kind: "direct", Limit: 100})
		if err != nil {
			return false
		}
		for _, c := range chats {
			if c.Address != "bob@localhost" {
				continue
			}
			messages, err := engine.Messages(ctx, c.ID, "", 100)
			if err != nil {
				continue
			}
			for _, message := range messages {
				if message.OriginalText == body {
					conversation, source = c, message
					return true
				}
			}
		}
		return false
	})
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(handler)
	defer api.Close()
	client, err := engineapi.NewClientWithResponses(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	read := func() *engineapi.MessageReactions {
		t.Helper()
		response, err := client.GetMessageReactionsWithResponse(ctx, string(source.ID))
		if err != nil || response.JSON200 == nil {
			t.Fatal("reaction HTTP snapshot", err)
		}
		return response.JSON200
	}
	if snapshot := read(); !snapshot.Eligible || snapshot.MessageId != string(source.ID) || snapshot.AccountId != string(registered.Account.ID) || snapshot.ConversationId != string(conversation.ID) || len(snapshot.Items) != 0 || snapshot.OwnRevisionId != nil {
		t.Fatal("initial scoped reaction eligibility", snapshot)
	}
	sendRemote := func(emojis []string) {
		t.Helper()
		payload := `<reactions xmlns="urn:xmpp:reactions:0" id="` + sourceWireID + `">`
		for _, emoji := range emojis {
			payload += `<reaction>` + stanzaText(emoji) + `</reaction>`
		}
		payload += `</reactions><store xmlns="urn:xmpp:hints"/>`
		if err := seed.Send(ctx, stanza.Message{To: to, Type: stanza.ChatMessage, ID: "reaction-event-" + string(id())}.Wrap(xml.NewDecoder(strings.NewReader(payload)))); err != nil {
			t.Fatal(err)
		}
	}
	sendRemote([]string{"👍"})
	eventually(t, func() bool {
		snapshot := read()
		return len(snapshot.Items) == 1 && snapshot.Items[0].Emoji == "👍" && snapshot.Items[0].Count == 1 && !snapshot.Items[0].Own
	})
	for _, own := range [][]string{{"👍"}, {"❤️"}, {}} {
		before := read()
		key := string(id())
		response, err := client.SetMessageReactionsWithResponse(ctx, string(source.ID), &engineapi.SetMessageReactionsParams{IdempotencyKey: key}, engineapi.SetMessageReactionsJSONRequestBody{ExpectedOwnRevisionId: before.OwnRevisionId, Emojis: append(engineapi.ReactionEmojiSet{}, own...)})
		if err != nil || response.JSON202 == nil {
			t.Fatal("reaction command acceptance", err, response)
		}
		select {
		case msg := <-wire:
			if msg.ID != key || msg.Type != stanza.ChatMessage || msg.Body != "" || msg.Reactions[0].ID != sourceWireID || !slices.Equal(msg.Reactions[0].Items, own) {
				t.Fatal("reaction command did not preserve bodyless set/target", msg)
			}
		case <-ctx.Done():
			t.Fatal("reaction wire missing")
		}
		eventually(t, func() bool {
			snapshot := read()
			return snapshot.PendingCommand == nil && reflect.DeepEqual([]string(snapshot.OwnEmojis), own)
		})
		receipt, err := client.GetMessageReactionCommandWithResponse(ctx, key)
		if err != nil || receipt.JSON200 == nil || receipt.JSON200.State != "submitted" {
			t.Fatal("durable submitted reaction receipt", err)
		}
		current, err := engine.Message(ctx, source.ID)
		if err != nil || current.RevisionID != source.RevisionID || current.OriginalText != body {
			t.Fatal("reaction changed message content/revision", err)
		}
		if _, err = engine.Message(ctx, domain.ID(key)); !errors.Is(err, application.ErrNotFound) {
			t.Fatal("reaction command materialized as chat message", err)
		}
	}
	sendRemote([]string{"👋"})
	eventually(t, func() bool {
		snapshot := read()
		return len(snapshot.Items) == 1 && snapshot.Items[0].Emoji == "👋" && snapshot.Items[0].Count == 1 && !snapshot.Items[0].Own
	})
	sendRemote([]string{})
	eventually(t, func() bool { return len(read().Items) == 0 })
}
