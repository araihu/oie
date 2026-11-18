package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func seedPortableRoomMessage(t *testing.T, s *Store, c domain.Conversation, at time.Time, body, provider, sid, mam string) domain.Message {
	t.Helper()
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID,
		Sender: domain.Sender{DisplayName: "Bob"}, OriginalText: body, CreatedAt: at, SendState: "received", ProviderID: provider}
	if sid != "" {
		m.References = []domain.MessageReference{{Scope: "room:" + c.Address, Author: c.Address + "/Bob", ID: sid}}
	}
	if mam != "" {
		m.Historical = true
		m.ArchiveScope = c.Address
		m.ArchiveID = mam
	}
	if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBackupPortableXMPPIdentity(t *testing.T) {
	left, _, lc := outboxFixture(t)
	right, _, rc := outboxFixture(t)
	_, lc = promoteBackupRoomFixture(t, left, lc)
	_, rc = promoteBackupRoomFixture(t, right, rc)
	at := time.Now().UTC()
	l := seedPortableRoomMessage(t, left, lc, at, "hello", "live-row", "room-sid", "")
	r := seedPortableRoomMessage(t, right, rc, at, "hello", "archive-row", "room-sid", "mam-42")
	if l.ID == r.ID || lc.ID == rc.ID || lc.AccountID == rc.AccountID {
		t.Fatal("fixture shares local IDs")
	}
	newer := addBackupMessageRevision(t, left, l, "edited", at.Add(time.Hour))
	zipL, dbL, tmpL := backupRestoreFixture(t, left, false)
	zipR, dbR, tmpR := backupRestoreFixture(t, right, false)
	for _, tc := range []struct {
		s           *Store
		c           domain.Conversation
		m           domain.Message
		manifest    backupManifest
		db, scratch string
	}{{left, lc, l, zipR, dbR, tmpR}, {right, rc, r, zipL, dbL, tmpL}} {
		for range 2 {
			summary, err := tc.s.mergeBackup(t.Context(), tc.db, tc.scratch, tc.manifest, at)
			if err != nil || summary != (RestoreSummary{}) {
				t.Fatalf("merge: %+v %v", summary, err)
			}
		}
		current, err := tc.s.Message(t.Context(), tc.m.ID)
		if err != nil || current.OriginalText != "edited" || current.RevisionID != newer.RevisionID || current.ConversationID != tc.c.ID {
			t.Fatalf("current message: %+v %v", current, err)
		}
		var messages, keys int
		if err := tc.s.db.QueryRow("SELECT (SELECT count(*) FROM messages),(SELECT count(DISTINCT key_type) FROM xmpp_message_keys WHERE message_id=?)", tc.m.ID).Scan(&messages, &keys); err != nil || messages != 1 || keys != 2 {
			t.Fatalf("messages=%d keys=%d err=%v", messages, keys, err)
		}
	}
	relay, relayDB, relayTmp := backupRestoreFixture(t, left, false)
	if _, err := right.mergeBackup(t.Context(), relayDB, relayTmp, relay, at); err != nil {
		t.Fatal(err)
	}
	if _, err := right.db.Exec("DELETE FROM messages WHERE id=?", r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := right.mergeBackup(t.Context(), dbL, tmpL, zipL, at); err != nil {
		t.Fatal(err)
	}
	seedPortableRoomMessage(t, right, rc, at, "hello", "unrelated", "", "mam-42")
	if _, err := right.Message(t.Context(), r.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("deleted occurrence returned: %v", err)
	}
	var count int
	if err := right.db.QueryRow("SELECT count(*) FROM messages").Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted messages=%d err=%v", count, err)
	}
}

func TestBackupPortableIdentityRequiresReview(t *testing.T) {
	left, _, lc := outboxFixture(t)
	right, _, rc := outboxFixture(t)
	_, lc = promoteBackupRoomFixture(t, left, lc)
	_, rc = promoteBackupRoomFixture(t, right, rc)
	at := time.Now().UTC()
	l := seedPortableRoomMessage(t, left, lc, at, "ok", "a", "sid-a", "")
	seedPortableRoomMessage(t, right, rc, at, "ok", "b", "", "mam-b")
	manifest, db, scratch := backupRestoreFixture(t, right, false)
	if _, err := left.mergeBackup(t.Context(), db, scratch, manifest, at); err == nil || !strings.Contains(err.Error(), string(l.ID)) {
		t.Fatalf("missing review: %v", err)
	}
}

func TestBackupPortableReviewKeepsSplitKeysAndQueueUntouched(t *testing.T) {
	source, _, sc := outboxFixture(t)
	dest, _, dc := outboxFixture(t)
	_, sc = promoteBackupRoomFixture(t, source, sc)
	_, dc = promoteBackupRoomFixture(t, dest, dc)
	at := time.Now().UTC()
	in := seedPortableRoomMessage(t, source, sc, at, "one", "s", "sid-1", "mam-2")
	seedPortableRoomMessage(t, dest, dc, at, "one", "d1", "sid-1", "mam-1")
	seedPortableRoomMessage(t, dest, dc, at, "two", "d2", "sid-2", "mam-2")
	manifest, db, scratch := backupRestoreFixture(t, source, false)
	_, before, err := dest.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(t.Context(), db, scratch, manifest, at); err == nil || !strings.Contains(err.Error(), string(in.ID)) {
		t.Fatalf("split keys: %v", err)
	}
	if _, after, err := dest.Snapshot(t.Context()); err != nil || after != before {
		t.Fatalf("failed merge changed destination: %d %v", after, err)
	}
}

func TestBackupPortableQueueAndRemovedAccount(t *testing.T) {
	source, engine, sc := outboxFixture(t)
	dest, _, dc := outboxFixture(t)
	ctx := t.Context()
	at := time.Now().UTC()
	item := queueBackupFixture(t, engine, sc.ID, "review on original owner", nil)
	manifest, db, scratch := backupRestoreFixture(t, source, false)
	if result, err := dest.mergeBackup(ctx, db, scratch, manifest, at); err != nil || result.PausedSends != 1 {
		t.Fatalf("merge queue: %+v %v", result, err)
	}
	held, err := dest.RecoveredSends(ctx)
	if err != nil || len(held) != 1 || held[0].MessageID != item.ID || held[0].AccountID != dc.AccountID || held[0].Destination != dc.Address || held[0].SourceEngineID != manifest.SourceEngine {
		t.Fatalf("recovered queue: %+v %v", held, err)
	}
	if dispatch, err := dest.PendingMessages(ctx, at.Add(24*time.Hour)); err != nil || len(dispatch) != 0 {
		t.Fatalf("restored send dispatchable: %+v %v", dispatch, err)
	}
	if _, err := dest.RemoveAccount(ctx, dc.AccountID, at); err != nil {
		t.Fatal(err)
	}
	if result, err := dest.mergeBackup(ctx, db, scratch, manifest, at); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("restored removed XMPP account: %+v %v", result, err)
	}
}

func TestLiveRoomOccurrenceLearnsMAMKey(t *testing.T) {
	store, _, chat := outboxFixture(t)
	_, chat = promoteBackupRoomFixture(t, store, chat)
	at := time.Now().UTC()
	first := seedPortableRoomMessage(t, store, chat, at, "same", "live-client", "sid", "")
	seedPortableRoomMessage(t, store, chat, at, "same", "other-client", "sid", "archive")
	seedPortableRoomMessage(t, store, chat, at, "same", "third-client", "", "archive")
	var rows, keys int
	err := store.db.QueryRow("SELECT (SELECT COUNT(*) FROM messages),(SELECT COUNT(DISTINCT key_type) FROM xmpp_message_keys WHERE message_id=?)", first.ID).Scan(&rows, &keys)
	if err != nil || rows != 1 || keys != 2 {
		t.Fatalf("live and MAM rows=%d keys=%d err=%v", rows, keys, err)
	}
}

func TestBackupPortableWeakDeletionRequiresReview(t *testing.T) {
	source, _, sc := outboxFixture(t)
	destination, _, dc := outboxFixture(t)
	at := time.Now().UTC()
	removed := replySeed(t, destination, dc, "same", at, nil, nil)
	if _, err := destination.db.Exec("DELETE FROM messages WHERE id=?", removed.ID); err != nil {
		t.Fatal(err)
	}
	incoming := replySeed(t, source, sc, "same", at, nil, nil)
	manifest, db, scratch := backupRestoreFixture(t, source, false)
	if _, err := destination.mergeBackup(t.Context(), db, scratch, manifest, at); err == nil || !strings.Contains(err.Error(), string(incoming.ID)) {
		t.Fatalf("weak exclusion: %v", err)
	}
}
