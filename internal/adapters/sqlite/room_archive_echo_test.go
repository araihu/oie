package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestRoomArchiveOwnEchoInEitherOrder(t *testing.T) {
	for _, archiveFirst := range []bool{true, false} {
		for _, knownOwn := range []bool{true, false} {
			store, _, chat := outboxFixture(t)
			e, chat := promoteBackupRoomFixture(t, store, chat)
			sent := queueBackupFixture(t, e, chat.ID, "echo text", nil)
			const key = "room-issued-id"
			own := application.Incoming{AccountID: chat.AccountID, Kind: "room", Address: chat.Address,
				SenderName: "Tester", IsOwn: true, CreatedAt: time.Now().UTC(),
				OriginalText: sent.OriginalText, CanonicalID: sent.ID, ProviderID: string(sent.ID),
				References: []domain.MessageReference{{Scope: "room:" + chat.Address, Author: chat.Address + "/Tester", ID: key}}}
			mam := own
			mam.CanonicalID = ""
			mam.ProviderID = "room:sender-client-id"
			mam.IsOwn = knownOwn
			if knownOwn {
				mam.SenderAddress = "alice@localhost"
			}
			mam.Historical = true
			mam.ArchiveScope = chat.Address
			mam.ArchiveID = key
			packets := []application.Incoming{own, mam, own, mam}
			if archiveFirst {
				packets = []application.Incoming{mam, own, mam, own}
			}
			for _, p := range packets {
				if err := e.Receive(t.Context(), p); err != nil {
					t.Fatalf("archiveFirst=%v knownOwn=%v: %v", archiveFirst, knownOwn, err)
				}
			}
			rows, err := store.Messages(t.Context(), chat.ID, "", 10)
			if err != nil || len(rows) != 1 || rows[0].ID != sent.ID || !rows[0].IsOwn {
				t.Fatalf("archiveFirst=%v knownOwn=%v got %+v, %v", archiveFirst, knownOwn, rows, err)
			}
		}
	}
}

func TestRoomArchiveEchoRepairedAfterUpgradeAndRestore(t *testing.T) {
	store, _, chat := outboxFixture(t)
	e, chat := promoteBackupRoomFixture(t, store, chat)
	sent := queueBackupFixture(t, e, chat.ID, "archived text", nil)
	if _, err := store.TransitionMessage(t.Context(), sent.ID, "queued", "submitted", migrationID(), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	const uid = "room-assigned"
	ref := domain.MessageReference{Scope: "room:" + chat.Address, Author: chat.Address + "/Bob", ID: uid}
	own := application.Incoming{AccountID: chat.AccountID, Kind: "room", Address: chat.Address,
		SenderName: "Bob", IsOwn: true, CreatedAt: time.Now().UTC(),
		OriginalText: sent.OriginalText, CanonicalID: sent.ID, ProviderID: string(sent.ID),
		References: []domain.MessageReference{ref}}
	if err := e.Receive(t.Context(), own); err != nil {
		t.Fatal(err)
	}
	archived := seedPortableRoomMessage(t, store, chat, own.CreatedAt, sent.OriginalText, "room:legacy", "", uid)
	star, err := e.SetMessageStar(t.Context(), migrationID(), archived.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	migrateFixtureToVersion(t, store, 54)
	migrateFixtureToVersion(t, store, backupV1Schema)
	assertLink := func(s *Store) {
		t.Helper()
		var root string
		if err := s.db.QueryRow("SELECT original_id FROM room_archive_echo_links WHERE echo_id=?", archived.ID).Scan(&root); err != nil || root != string(sent.ID) {
			t.Fatalf("room link %q: %v", root, err)
		}
		starred, err := s.MessageStar(t.Context(), sent.ID)
		if err != nil || !starred.Starred || starred.RevisionID != star.Star.RevisionID {
			t.Fatalf("inherited star: %+v %v", starred, err)
		}
		rows, err := s.Messages(t.Context(), chat.ID, "", 10)
		if err != nil || len(rows) != 1 || rows[0].ID != sent.ID {
			t.Fatalf("visible rows: %+v, %v", rows, err)
		}
	}
	assertLink(store)
	manifest, db, scratch := backupRestoreFixture(t, store, false)
	dest, err := Open(t.Context(), t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dest.Close() })
	for range 2 {
		if _, err := dest.mergeBackup(t.Context(), db, scratch, manifest, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		assertLink(dest)
	}
	for _, s := range []*Store{store, dest} {
		conflict := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: chat.AccountID,
			Sender: domain.Sender{DisplayName: "Other"}, CreatedAt: own.CreatedAt, SendState: "received",
			OriginalText: sent.OriginalText,
			Historical:   true, ArchiveScope: chat.Address, ArchiveID: uid, ProviderID: "room:disputed",
			References: []domain.MessageReference{{Scope: ref.Scope, ID: uid, Author: chat.Address + "/Other"}}}
		for range 2 {
			if err := s.ReceiveMessage(t.Context(), chat, conflict, migrationID()); err != nil {
				t.Fatal(err)
			}
			rows, err := s.Messages(t.Context(), chat.ID, "", 10)
			if err != nil || len(rows) != 2 {
				t.Fatalf("disputed rows: %d, %v", len(rows), err)
			}
		}
	}
	if _, err := dest.mergeBackup(t.Context(), db, scratch, manifest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := dest.db.QueryRow("SELECT COUNT(*) FROM room_archive_echo_links").Scan(&links); err != nil || links != 0 {
		t.Fatalf("old backup revived disputed pair: %d, %v", links, err)
	}
}

func TestRoomArchiveDifferentOccupantsAndConflictingIDs(t *testing.T) {
	store, _, chat := outboxFixture(t)
	_, chat = promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	first := seedPortableRoomMessage(t, store, chat, at, "same", "first", "sid-of-first", "uid")
	live := seedPortableRoomMessage(t, store, chat, at, "same", "second", "uid", "")
	rows, err := store.Messages(t.Context(), chat.ID, "", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("different explicit stanza IDs: %d, %v", len(rows), err)
	}
	// Eve's copy must never give Bob an addressable reference in her name.
	eve := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: chat.AccountID,
		Sender: domain.Sender{DisplayName: "Eve"}, OriginalText: "same", CreatedAt: at,
		SendState: "received", ProviderID: "eve",
		Historical: true, ArchiveScope: chat.Address, ArchiveID: "different-mam",
		References: []domain.MessageReference{{Scope: "room:" + chat.Address, Author: chat.Address + "/Eve", ID: "uid"}}}
	if err := store.ReceiveMessage(t.Context(), chat, eve, migrationID()); err != nil {
		t.Fatal(err)
	}
	var attached bool
	if err := store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM message_references WHERE message_id IN (?,?) AND author=?)", first.ID, live.ID, chat.Address+"/Eve").Scan(&attached); err != nil || attached {
		t.Fatalf("incorrect occupant attached: %v, %v", attached, err)
	}
}

func TestRoomArchiveConflictingPersonalAddressKeepsAuthor(t *testing.T) {
	store, _, chat := outboxFixture(t)
	_, chat = promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	old := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: chat.AccountID,
		Sender: domain.Sender{DisplayName: "Bob", PersonalAddress: "bob@elsewhere.test"}, OriginalText: "same", CreatedAt: at,
		SendState: "received", ProviderID: "old",
		Historical: true, ArchiveScope: chat.Address, ArchiveID: "uid"}
	if err := store.ReceiveMessage(t.Context(), chat, old, migrationID()); err != nil {
		t.Fatal(err)
	}
	live := old
	live.ID, live.RevisionID, live.IsOwn = migrationID(), migrationID(), true
	live.Sender.PersonalAddress, live.ArchiveScope, live.ArchiveID, live.Historical = "", "", "", false
	live.ProviderID, live.SendState = "own", "submitted"
	live.References = []domain.MessageReference{{Scope: "room:" + chat.Address, Author: chat.Address + "/Bob", ID: "uid"}}
	if err := store.ReceiveMessage(t.Context(), chat, live, migrationID()); err != nil {
		t.Fatal(err)
	}
	got, err := store.Message(t.Context(), old.ID)
	if err != nil || got.IsOwn {
		t.Fatalf("archive sender changed: %+v %v", got.Sender, err)
	}
}
