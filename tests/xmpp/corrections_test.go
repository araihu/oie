package xmpp_test

import (
	"context"
	"crypto/x509"
	"encoding/xml"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	limit := 1
	first, err := client.GetMessageEditHistoryWithResponse(ctx, string(source.ID), &engineapi.GetMessageEditHistoryParams{Limit: &limit})
	if err != nil || first.JSON200 == nil || len(first.JSON200.Items) != 1 || first.JSON200.NextCursor == nil {
		t.Fatal("bounded observed history unavailable", err)
	}
	page := first.JSON200
	if page.MessageId != string(source.ID) || page.RequestedMessageId != string(source.ID) || page.AccountId != string(registered.Account.ID) || page.ConversationId != string(conversation.ID) || page.CurrentRevisionId != string(current.RevisionID) || len(page.Snapshot) != 64 {
		t.Fatal("history lost canonical source scope")
	}
	older, err := client.GetMessageEditHistoryWithResponse(ctx, string(source.ID), &engineapi.GetMessageEditHistoryParams{Limit: &limit, Cursor: page.NextCursor})
	if err != nil || older.JSON200 == nil || len(older.JSON200.Items) != 1 || older.JSON200.NextCursor != nil || older.JSON200.Snapshot != page.Snapshot {
		t.Fatal("bounded older history unavailable", err)
	}
	seen := map[string]engineapi.MessageEditHistoryItem{}
	for _, version := range append(page.Items, older.JSON200.Items...) {
		seen[version.OriginalText] = version
	}
	if len(seen) != 2 || !seen[original].IsOriginal || seen[original].IsCurrent || !seen[corrected].IsCurrent || seen[corrected].IsOriginal {
		t.Fatal("observed original/current labels or retained source bodies lost")
	}
	if seen[corrected].RevisionId != page.CurrentRevisionId || len(seen[corrected].ParentRevisionIds) != 1 || seen[corrected].ParentRevisionIds[0] != seen[original].RevisionId {
		t.Fatal("content ancestry missing")
	}
	// A retained correction alias resolves to the same logical source history.
	alias := seen[corrected].SourceMessageId
	aliased, err := client.GetMessageEditHistoryWithResponse(ctx, alias, &engineapi.GetMessageEditHistoryParams{})
	if err != nil || aliased.JSON200 == nil || aliased.JSON200.MessageId != string(source.ID) || aliased.JSON200.RequestedMessageId != alias || aliased.JSON200.Snapshot != page.Snapshot {
		t.Fatal("correction alias did not resolve scoped history", err)
	}
	// Every correction uses the original client stanza ID, including later edits.
	secondBody := strings.Replace(corrected, "Corrected XEP-0308", "Second correction", 1)
	send("correction-second-"+string(id()), secondBody, originalID)
	eventually(t, func() bool {
		next, err := engine.Message(ctx, source.ID)
		return err == nil && next.OriginalText == secondBody
	})
	stale, err := client.GetMessageEditHistoryWithResponse(ctx, string(source.ID), &engineapi.GetMessageEditHistoryParams{Limit: &limit, Cursor: page.NextCursor})
	if err != nil || stale.StatusCode() != 409 {
		t.Fatal("changed content history accepted stale cursor", err)
	}
	fresh, err := client.GetMessageEditHistoryWithResponse(ctx, string(source.ID), &engineapi.GetMessageEditHistoryParams{})
	if err != nil || fresh.JSON200 == nil || len(fresh.JSON200.Items) != 3 || fresh.JSON200.Snapshot == page.Snapshot {
		t.Fatal("new observed correction did not refresh history", err)
	}
	pending, err := store.PendingMessages(ctx, time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("received correction/history read created outbox work", err)
	}
}
