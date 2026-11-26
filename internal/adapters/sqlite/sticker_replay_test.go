package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestIdlessOwnStickerReplayRetainsMessageIdentity(t *testing.T) {
	store, engine, chat := outboxFixture(t)
	engine.SetProtocols(&outboundProtocol{})
	origin := migrationID()
	const url = "https://files.example.org/sticker.gif"
	original := application.Incoming{
		AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", IsOwn: true, CanonicalID: origin,
		CreatedAt: time.Now().UTC(), OriginalText: url, WireMentions: domain.WireMentionsAbsent,
		FileReferences: []domain.SharedReference{{Kind: "image", URL: url}},
		References:     []domain.MessageReference{{Scope: "sender:alice@localhost", Author: "alice@localhost", ID: string(origin)}},
	}
	if err := engine.Receive(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	read := func() domain.Message {
		t.Helper()
		rows, err := store.Messages(t.Context(), chat.ID, "", 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("retained messages: %v, %v", rows, err)
		}
		return rows[0]
	}
	before := read()
	original.ArchiveScope, original.ArchiveID, original.Historical = "alice@localhost", "archive-sticker", true
	original.ProviderID = "archive:" + bodyHash(original.ArchiveScope+"\x00"+original.ArchiveID)
	original.FileReferences[0].Sticker = true
	if err := engine.Receive(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	after := read()
	if before.ID != after.ID || after.RevisionID == before.RevisionID || !after.FileReferences[0].Sticker {
		t.Fatalf("replayed sticker %v", after)
	}
}
