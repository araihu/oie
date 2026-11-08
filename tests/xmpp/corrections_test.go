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

func TestReceivedCorrectionWirePreservesLogicalMessage(t *testing.T) {
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
	go func() {
		_ = seed.Serve(mx.HandlerFunc(func(_ xmlstream.TokenReadEncoder, _ *xml.StartElement) error { return nil }))
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
	registered, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Received correction fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.ConfigureConnection(ctx, registered.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		connection, err := engine.Connection(ctx, registered.Account.ID)
		return err == nil && connection.State == "connected"
	})
	to, _ := jid.Parse("alice@localhost")
	originalID, editID := "correction-original-"+string(id()), "correction-edit-"+string(id())
	original := "Original café 🧑🏽‍💻 " + string(id())
	corrected := strings.Replace(original, "Original", "Corrected XEP-0308", 1)
	send := func(messageID, body, replace string) {
		t.Helper()
		payload := `<body>` + stanzaText(body) + `</body>`
		if replace != "" {
			payload += `<replace xmlns="urn:xmpp:message-correct:0" id="` + replace + `"/>`
		}
		if err := seed.Send(ctx, stanza.Message{To: to, Type: stanza.ChatMessage, ID: messageID}.Wrap(xml.NewDecoder(strings.NewReader(payload)))); err != nil {
			t.Fatal(err)
		}
	}
	send(originalID, original, "")
	var conversation domain.Conversation
	var source domain.Message
	eventually(t, func() bool {
		chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: registered.Account.ID, Kind: "direct", Limit: 100})
		if err != nil {
			return false
		}
		for _, chat := range chats {
			if chat.Address != "bob@localhost" {
				continue
			}
			messages, err := engine.Messages(ctx, chat.ID, "", 100)
			if err != nil {
				return false
			}
			for _, message := range messages {
				if message.OriginalText == original {
					conversation = chat
					source = message
					return true
				}
			}
		}
		return false
	})
	send(editID, corrected, originalID)
	var current domain.Message
	eventually(t, func() bool {
		messages, err := engine.Messages(ctx, conversation.ID, "", 100)
		if err != nil {
			return false
		}
		matches := []domain.Message{}
		for _, message := range messages {
			if message.OriginalText == original || message.OriginalText == corrected {
				matches = append(matches, message)
			}
		}
		if len(matches) != 1 || matches[0].OriginalText != corrected || !matches[0].Edited {
			return false
		}
		current = matches[0]
		return true
	})
	if current.ID != source.ID || current.RevisionID == source.RevisionID || !current.CreatedAt.Equal(source.CreatedAt) || current.EditedAt.IsZero() {
		t.Fatal("correction replaced logical identity or lost revision evidence", current.ID, current.RevisionID)
	}
	send(editID, corrected, originalID)
	time.Sleep(200 * time.Millisecond)
	replay, err := engine.Message(ctx, source.ID)
	if err != nil || replay.RevisionID != current.RevisionID || replay.OriginalText != corrected {
		t.Fatal("wire replay created another correction revision", err)
	}
	pending, err := store.PendingMessages(ctx, time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("received correction created outbox work", err)
	}
}
