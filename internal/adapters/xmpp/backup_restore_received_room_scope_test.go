package xmpp

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreReceivedRoomCorrectionsAtReusedNicknameRemainIndependent(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	store, engine, _ := openDirectEchoProfile(t, dir)
	account, _ := setupDirectEchoProfile(t, engine)
	opened, err := engine.OpenRoom(ctx, mintDirectEchoID(t), account.ID, "room@conference.localhost", "Alice", "", false)
	if err != nil {
		t.Fatal(err)
	}
	room := opened.Conversation
	author := room.Address + "/ReusedNickname"
	now := time.Now().UTC()
	observe := func(engine *application.Engine, actor, jid, archiveKey, body string, id, target domain.ID) string {
		t.Helper()
		replace := ""
		if target != "" {
			replace = fmt.Sprintf(`<replace xmlns="urn:xmpp:message-correct:0" id="%s"/>`, target)
		}
		raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s" type="groupchat" id="%s"><body>%s</body>%s<occupant-id xmlns="urn:xmpp:occupant-id:0" id="%s"/><x xmlns="http://jabber.org/protocol/muc#user"><item jid="%s/resource"/></x></message></forwarded></result>`,
			archiveKey, now.Format(time.RFC3339Nano), author, id, escaped(body), replace, actor, jid)
		var result archiveResult
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		in, ok, err := archivedIncoming(account, room.Address, &result, archiveTrust{SIDVerified: true, OccupantVerified: true})
		if err != nil || !ok || in.IsOwn || in.Correction == nil || in.Correction.Scope != roomCorrectionScope(room.Address, author, actor) {
			t.Fatalf("wrong received room fixture: %+v, %v, %v", in, ok, err)
		}
		if err := engine.Receive(ctx, in); err != nil {
			t.Fatal(err)
		}
		return in.ProviderID
	}
	readDatabase := func(dir string) *sql.DB {
		t.Helper()
		db, err := sql.Open("sqlite", filepath.Join(dir, "oie.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		})
		return db
	}
	firstOriginal, secondOriginal, sharedEdit := mintDirectEchoID(t), mintDirectEchoID(t), mintDirectEchoID(t)
	const firstBody, secondBody = "the first corrected text", "the second corrected text"
	observe(engine, "first-actor", "bob@localhost", "first-initial", "the first original", firstOriginal, "")
	legacyProvider := observe(engine, "first-actor", "bob@localhost", "first-correction", firstBody, sharedEdit, firstOriginal)
	items, err := store.Messages(ctx, room.ID, "", 10)
	if err != nil || len(items) != 1 || !items[0].Edited {
		t.Fatalf("initial archive did not project a correction: %+v, %v", items, err)
	}
	first := items[0]
	restoreLegacyRoomProviderKey(t, readDatabase(dir), first.ProjectionSourceID, legacyProvider)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	backup := writeRoomEchoBackup(t, dir)
	dest := t.TempDir()
	if _, err := sqlite.RestoreProfileBackup(ctx, dest, backup, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	restored, receiver, _ := openDirectEchoProfile(t, dest)
	observe(receiver, "second-actor", "charlie@localhost", "second-initial", "the second original", secondOriginal, "")
	observe(receiver, "second-actor", "charlie@localhost", "second-correction", secondBody, sharedEdit, secondOriginal)
	observe(receiver, "first-actor", "bob@localhost", "first-correction-again", firstBody, sharedEdit, firstOriginal)
	observe(receiver, "second-actor", "charlie@localhost", "second-correction-again", secondBody, sharedEdit, secondOriginal)
	check := func(store *sqlite.Store, dir string) {
		t.Helper()
		items, err := store.Messages(ctx, room.ID, "", 10)
		if err != nil || len(items) != 2 || !items[0].Edited || !items[1].Edited || items[0].ProjectionSourceID == items[1].ProjectionSourceID {
			t.Fatalf("an occupant's reused nickname and client ID changed the rendered corrections: %+v, %v", items, err)
		}
		byBody := map[string]domain.Message{items[0].OriginalText: items[0], items[1].OriginalText: items[1]}
		kept, next := byBody[firstBody], byBody[secondBody]
		if kept.ID != first.ID || kept.RevisionID != first.RevisionID || kept.ProjectionSourceID != first.ProjectionSourceID || next.ID == "" {
			t.Fatalf("the legacy correction lost its immutable source or the new occupant's correction was lost: %+v", items)
		}
		if raw, err := store.MessageSource(ctx, kept.ProjectionSourceID); err != nil || raw.ProviderID != legacyProvider {
			t.Fatalf("the first observation no longer has its authentic legacy provider: %+v, %v", raw, err)
		}
		if raw, err := store.MessageSource(ctx, next.ProjectionSourceID); err != nil || raw.ProviderID == legacyProvider {
			t.Fatalf("the second actor inherited the first actor's provider key: %+v, %v", raw, err)
		}
		db := readDatabase(dir)
		for key, sourceID := range map[string]domain.ID{
			"first-correction": kept.ProjectionSourceID, "first-correction-again": kept.ProjectionSourceID,
			"second-correction": next.ProjectionSourceID, "second-correction-again": next.ProjectionSourceID,
		} {
			if owner := readRoomCorrectionArchiveOwner(t, db, account.ID, room.Address, key); owner != sourceID {
				t.Fatalf("archive key %q changed owner: got %s, want %s", key, owner, sourceID)
			}
		}
	}
	check(restored, dest)
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
	fresh := t.TempDir()
	if _, err := sqlite.RestoreProfileBackup(ctx, fresh, writeRoomEchoBackup(t, dest), mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	restored, _, _ = openDirectEchoProfile(t, fresh)
	check(restored, fresh)
}
