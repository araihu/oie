package xmpp

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestDirectOwnOriginDeduplicatesLiveThenArchiveWithoutStanzaID(t *testing.T) {
	ctx := t.Context()
	newid := func() (domain.ID, error) { id, err := uuid.NewV7(); return domain.ID(id.String()), err }
	mint := func() domain.ID {
		id, err := newid()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir, mint())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	engine, err := application.New(store, "local", newid, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := New(dir, nil)
	manager.host = engine
	engine.SetProtocols(manager)
	registered, err := engine.Register(ctx, mint(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	account := registered.Account
	chat, err := engine.OpenConversation(ctx, mint(), account.ID, account.Address)
	if err != nil {
		t.Fatal(err)
	}
	origin := mint()
	const body = "text sent from another signed-in resource"
	packet := func(body, options string) string {
		return fmt.Sprintf(`<message xmlns="jabber:client" from="%s/other-client" to="%s" type="chat"><body>%s</body><origin-id xmlns="urn:xmpp:sid:0" id="%s"/>%s</message>`, account.Address, account.Address, body, origin, options)
	}
	decoder := xml.NewDecoder(strings.NewReader(packet(body, "")))
	token, err := decoder.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := token.(xml.StartElement)
	if err := manager.handle(ctx, &entry{account: account}, tokens{decoder, xml.NewEncoder(&bytes.Buffer{})}, &start); err != nil {
		t.Fatal(err)
	}
	messages, err := store.Messages(ctx, chat.Conversation.ID, "", 10)
	if err != nil || len(messages) != 1 || messages[0].OwnOriginID != origin {
		t.Fatalf("fixture did not retain authenticated live origin: %+v %v", messages, err)
	}
	want := messages[0]
	db, err := sql.Open("sqlite", filepath.Join(dir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	observe := func(key, body, options string) domain.ID {
		t.Helper()
		raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/>%s</forwarded></result>`, key, time.Now().UTC().Format(time.RFC3339Nano), packet(body, options))
		var result archiveResult
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		in, ok, err := archivedIncoming(account, account.Address, &result, archiveTrust{})
		if err != nil || !ok || !in.IsOwn || in.CanonicalID != origin {
			t.Fatalf("fixture invalid authenticated historical copy: %+v %v %v", in, ok, err)
		}
		if err := engine.Receive(ctx, in); err != nil {
			t.Fatal(err)
		}
		var alias domain.ID
		if err := db.QueryRowContext(ctx, `SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?`, account.ID, in.ArchiveScope, key).Scan(&alias); err != nil {
			t.Fatal(err)
		}
		return alias
	}
	for range 2 {
		if alias := observe("archived", body, ""); alias != want.ID {
			t.Fatalf("MAM linked the archive key to %s instead of the retained live row %s", alias, want.ID)
		}
	}
	history, err := store.Messages(ctx, chat.Conversation.ID, "", 10)
	if err != nil || len(history) != 1 || history[0].ID != want.ID || history[0].RevisionID != want.RevisionID {
		t.Fatalf("MAM duplicated the same authenticated live origin: %d visible rows, %v", len(history), err)
	}
	changedBody := observe("changed-body", "different text", "")
	mention := `<reference xmlns="urn:xmpp:reference:0" type="mention" uri="xmpp:juliet@localhost" begin="0" end="4"/>`
	changedMention := observe("changed-mention", body, mention)
	history, err = store.Messages(ctx, chat.Conversation.ID, "", 10)
	if err != nil || len(history) != 3 || changedBody == want.ID || changedMention == want.ID || changedBody == changedMention {
		t.Fatalf("different wire content shared the live alias: body=%s mentions=%s rows=%d err=%v", changedBody, changedMention, len(history), err)
	}
}
