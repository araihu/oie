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

func TestRestoreRoomArchiveExclusionAcrossWireFormats(t *testing.T) {
	for _, tc := range []struct {
		name, deletedSID, deletedMAM, incomingSID, incomingMAM string
		keep                                                   bool
	}{
		{"deleted MAM, restored live", "", "uid", "uid", "", false},
		{"deleted live, restored MAM", "uid", "", "", "uid", false},
		{"distinct explicit MAM", "", "uid", "uid", "other", true},
		{"distinct explicit SID", "uid", "", "other", "uid", true},
		{"deleted with distinct explicit MAM", "uid", "other", "", "uid", true},
		{"deleted with distinct explicit SID", "other", "uid", "uid", "", true},
		{"archived UID also exposed as SID", "uid", "other", "uid", "uid", true},
		{"deleted UID also exposed as SID", "uid", "uid", "uid", "other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst, _, dc := outboxFixture(t)
			src, _, sc := outboxFixture(t)
			_, dc = promoteBackupRoomFixture(t, dst, dc)
			_, sc = promoteBackupRoomFixture(t, src, sc)
			at := time.Now().UTC()
			deleted := seedPortableRoomMessage(t, dst, dc, at, "same text", "local", tc.deletedSID, tc.deletedMAM)
			if _, err := dst.db.Exec("DELETE FROM messages WHERE id=?", deleted.ID); err != nil {
				t.Fatal(err)
			}
			incoming := seedPortableRoomMessage(t, src, sc, at, "same text", "remote", tc.incomingSID, tc.incomingMAM)
			manifest, archive, scratch := backupRestoreFixture(t, src, false)
			if _, err := dst.mergeBackup(t.Context(), archive, scratch, manifest, at); err != nil {
				t.Fatal(err)
			}
			remaining, err := dst.Messages(t.Context(), dc.ID, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if tc.keep {
				if len(remaining) != 1 || remaining[0].ID != incoming.ID {
					t.Fatalf("want distinct room occurrence %s, got %d rows", incoming.ID, len(remaining))
				}
			} else if len(remaining) != 0 {
				t.Fatal("restore resurrected the deleted room occurrence")
			}
		})
	}
}

// A replay suppressed solely by a matching UID cannot establish a second
// deletion key. A later observation can distinguish the two occurrences.
func TestInferredRoomExclusionCanBeDisambiguated(t *testing.T) {
	for _, restore := range []bool{false, true} {
		path := "receive"
		if restore {
			path = "restore"
		}
		t.Run(path, func(t *testing.T) {
			dst, _, chat := outboxFixture(t)
			_, chat = promoteBackupRoomFixture(t, dst, chat)
			at := time.Now().UTC()
			deleted := seedPortableRoomMessage(t, dst, chat, at, "old", "live", "uid", "")
			if _, err := dst.db.Exec("DELETE FROM messages WHERE id=?", deleted.ID); err != nil {
				t.Fatal(err)
			}
			deliver := func(sid string) {
				t.Helper()
				if !restore {
					seedPortableRoomMessage(t, dst, chat, at, "archive", "archive", sid, "uid")
					return
				}
				src, _, sc := outboxFixture(t)
				_, sc = promoteBackupRoomFixture(t, src, sc)
				seedPortableRoomMessage(t, src, sc, at, "archive", "archive", sid, "uid")
				manifest, archive, scratch := backupRestoreFixture(t, src, false)
				if _, err := dst.mergeBackup(t.Context(), archive, scratch, manifest, at); err != nil {
					t.Fatal(err)
				}
			}
			deliver("uid")
			if rows, err := dst.Messages(t.Context(), chat.ID, "", 10); err != nil || len(rows) != 0 {
				t.Fatalf("inferred replay became visible: %v, %v", len(rows), err)
			}
			deliver("different-sid")
			if rows, err := dst.Messages(t.Context(), chat.ID, "", 10); err != nil || len(rows) != 1 {
				t.Fatalf("distinct archive occurrence was lost: %v, %v", len(rows), err)
			}
		})
	}
}

func TestPortableRoomSIDCannotJoinDifferentArchiveOccurrences(t *testing.T) {
	left, _, lc := outboxFixture(t)
	right, _, rc := outboxFixture(t)
	_, lc = promoteBackupRoomFixture(t, left, lc)
	_, rc = promoteBackupRoomFixture(t, right, rc)
	at := time.Now().UTC()
	// The room's MAM projection fills a missing inner stanza ID with the MAM UID.
	seedPortableRoomMessage(t, left, lc, at, "hi", "left", "u", "v")
	seedPortableRoomMessage(t, right, rc, at.Add(time.Minute), "hi", "right", "u", "u")
	lm, ld, ls := backupRestoreFixture(t, left, false)
	rm, rd, rs := backupRestoreFixture(t, right, false)
	for _, tc := range []struct {
		dest        *Store
		chat        domain.Conversation
		manifest    backupManifest
		db, scratch string
	}{{left, lc, rm, rd, rs}, {right, rc, lm, ld, ls}} {
		if _, err := tc.dest.mergeBackup(t.Context(), tc.db, tc.scratch, tc.manifest, at); err != nil {
			t.Fatal(err)
		}
		rows, err := tc.dest.Messages(t.Context(), tc.chat.ID, "", 10)
		if err != nil || len(rows) != 2 {
			t.Fatalf("different room MAM occurrences merged: got %d, %v", len(rows), err)
		}
	}
}
