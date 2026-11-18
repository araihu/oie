package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsWitnessedEditsOfRetainedOlderRoomSend(t *testing.T) {
	t.Parallel()
	destination, _, placeholder := outboxFixture(t)
	sender, room := promoteBackupRoomFixture(t, destination, placeholder)
	sender.SetProtocols(&outboundProtocol{})
	witness, fresh := copyRestoreFixture(t, destination), copyRestoreFixture(t, destination)
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	pending := queueBackupFixture(t, sender, room.ID, "the old retained original", nil)
	at := pending.CreatedAt.Add(time.Minute)
	if _, err := destination.TransitionMessage(ctx, pending.ID, "queued", "dispatching", migrationID(), at, at); err != nil {
		t.Fatal(err)
	}
	original, err := destination.TransitionMessage(ctx, pending.ID, "dispatching", "submitted", migrationID(), at, at)
	if err != nil {
		t.Fatal(err)
	}
	root := domain.MessageCorrection{
		Scope: "xmpp:correct:room:" + room.Address + ":verified-occupant", Author: room.Address + "/Tester", ID: "root-stanza",
	}
	echo := application.Incoming{
		AccountID: room.AccountID, Address: room.Address, Kind: "room", Historical: true, IsOwn: true,
		SenderAddress: "alice@localhost", CreatedAt: at, OriginalText: original.OriginalText,
		ProviderID: "room:root", ArchiveScope: room.Address, ArchiveID: "archive-root", CanonicalID: pending.ID, Correction: &root,
	}
	if err := witnessEngine.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	outside := replySeed(t, witness, room, "an unrelated old body", at, nil, nil)
	recent := at.Add(48 * time.Hour)
	firstRef := domain.MessageReference{Scope: "room:" + room.Address, Author: root.Author, ID: "first-edit-stanza"}
	first := root
	first.ID, first.TargetID = firstRef.ID, root.ID
	echo.CreatedAt, echo.OriginalText, echo.ProviderID, echo.ArchiveID = recent, "the first witnessed edit", "room:first-edit", "archive-first-edit"
	echo.CanonicalID, echo.Correction, echo.References = "", &first, []domain.MessageReference{firstRef}
	if err := witnessEngine.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	firstID, err := witness.queries.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
		AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: echo.ArchiveID,
	})
	if err != nil {
		t.Fatal(err)
	}
	const corrected = "the verified chained edit near the restore"
	latest := root
	latest.ID, latest.TargetID = "second-edit-stanza", first.ID
	echo.CreatedAt, echo.OriginalText, echo.ProviderID, echo.ArchiveID = recent.Add(time.Second), corrected, "room:second-edit", "archive-second-edit"
	echo.Correction, echo.References = &latest, nil
	if err := witnessEngine.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	latestID, err := witness.queries.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
		AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: echo.ArchiveID,
	})
	if err != nil {
		t.Fatal(err)
	}
	witnessed, err := witness.Message(ctx, domain.ID(latestID))
	if err != nil || witnessed.OriginalText != corrected || !witnessed.Edited {
		t.Fatalf("witness did not project its verified correction chain: %+v, %v", witnessed, err)
	}
	days := 1
	for _, host := range []*application.Engine{sender, testRestoreEngine(t, fresh)} {
		if _, err := host.SetConversationRetention(ctx, room.ID, "0", &days); err != nil {
			t.Fatal(err)
		}
	}
	mergeAt := recent.Add(time.Minute)
	if n, err := destination.PurgeExpiredHistory(ctx, mergeAt, func() (domain.ID, error) { return migrationID(), nil }); err != nil || n != 0 {
		t.Fatalf("room minimum should retain the old native root: %d, %v", n, err)
	}
	archive, database, scratch := backupRestoreFixture(t, witness, false)
	if result, err := fresh.mergeBackup(ctx, database, scratch, archive, mergeAt); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("an older witness without a retained native root added fresh history: %+v, %v", result, err)
	}
	for _, expiredBy := range []string{"message ID", "scoped reference", "correction reference"} {
		t.Run(expiredBy, func(t *testing.T) {
			ctx := t.Context()
			store := copyRestoreFixture(t, destination)
			var err error
			switch expiredBy {
			case "message ID":
				_, err = store.db.ExecContext(ctx, `INSERT INTO expired_message_ids VALUES(?)`, firstID)
			case "scoped reference":
				_, err = store.db.ExecContext(ctx, `INSERT INTO expired_message_references VALUES(?,?,?,?,?)`,
					room.ID, firstRef.Scope, firstRef.ID, firstRef.Author, recent.UnixMilli())
			case "correction reference":
				_, err = store.db.ExecContext(ctx, `INSERT INTO expired_message_references VALUES(?,?,?,?,?)`,
					room.ID, first.Scope, first.ID, first.Author, recent.UnixMilli())
			}
			if err != nil {
				t.Fatal(err)
			}
			if result, err := store.mergeBackup(ctx, database, scratch, archive, mergeAt); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("restore revived the explicitly excluded edit or its descendant: %+v, %v", result, err)
			}
			if retained, err := store.Message(ctx, original.ID); err != nil || retained.Edited || retained.OriginalText != original.OriginalText {
				t.Fatalf("an excluded chain changed the original: %+v, %v", retained, err)
			}
			retention, err := store.ConversationRetention(ctx, room.ID)
			if err != nil {
				t.Fatal(err)
			}
			wider := 7
			if _, err := testRestoreEngine(t, store).SetConversationRetention(ctx, room.ID, retention.Revision, &wider); err != nil {
				t.Fatal(err)
			}
			if result, err := store.mergeBackup(ctx, database, scratch, archive, mergeAt); err != nil || result.Messages != 1 {
				t.Fatalf("widening should restore only the unrelated message: %+v, %v", result, err)
			}
			for _, id := range []domain.ID{domain.ID(firstID), domain.ID(latestID)} {
				if raw, err := store.MessageSource(ctx, id); !errors.Is(err, application.ErrNotFound) {
					t.Fatalf("widening restored excluded correction content: %q, %v", raw.OriginalText, err)
				}
			}
			if retained, err := store.Message(ctx, original.ID); err != nil || retained.RevisionID != original.RevisionID ||
				retained.Edited || retained.OriginalText != original.OriginalText {
				t.Fatalf("widening changed the retained original: %+v, %v", retained, err)
			}
			if retained, err := store.Message(ctx, outside.ID); err != nil || retained.OriginalText != outside.OriginalText {
				t.Fatalf("widening did not restore unrelated eligible content: %+v, %v", retained, err)
			}
		})
	}
	sameNative := copyRestoreFixture(t, destination)
	if result, err := destination.mergeBackup(ctx, database, scratch, archive, mergeAt); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("restore added visible messages alongside the retained root's edits: %+v, %v", result, err)
	}
	if stored, err := destination.MessageSource(ctx, original.ID); err != nil || stored.RevisionID != original.RevisionID || stored.OriginalText != original.OriginalText {
		t.Fatalf("restore changed the native source revision: %+v, %v", stored, err)
	}
	if stored, err := destination.Message(ctx, original.ID); err != nil || !stored.Edited || stored.OriginalText != corrected || stored.ProjectionSourceID != domain.ID(latestID) {
		t.Fatalf("the old native root lost its verified correction chain: %+v, %v", stored, err)
	}
	if _, err := destination.Message(ctx, outside.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("unrelated history escaped the cutoff: %v", err)
	}
	if edits, err := destination.ReadMessageEditHistory(ctx, original.ID, false); err != nil || len(edits.Items) != 3 {
		t.Fatalf("the native original or one of its edits is missing: %+v, %v", edits, err)
	}
	if result, err := destination.mergeBackup(ctx, database, scratch, archive, mergeAt); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("repeating the restore changed the corrected root: %+v, %v", result, err)
	}
	retainedChain := copyRestoreFixture(t, destination)
	storedEdit, err := retainedChain.MessageSource(ctx, domain.ID(latestID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retainedChain.db.ExecContext(ctx, `INSERT INTO expired_message_references VALUES(?,?,?,?,?)`,
		room.ID, first.Scope, first.ID, first.Author, recent.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if result, err := retainedChain.mergeBackup(ctx, database, scratch, archive, mergeAt); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("reapplying the archive changed a retained correction chain: %+v, %v", result, err)
	}
	if kept, err := retainedChain.MessageSource(ctx, domain.ID(latestID)); err != nil || kept.RevisionID != storedEdit.RevisionID ||
		kept.OriginalText != storedEdit.OriginalText {
		t.Fatalf("an exclusion removed the destination's retained correction: %+v, %v", kept, err)
	}
	relayed, relayedDB, relayedScratch := backupRestoreFixture(t, destination, false)
	if result, err := sameNative.mergeBackup(ctx, relayedDB, relayedScratch, relayed, mergeAt); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("re-export introduced visible rows beyond retention: %+v, %v", result, err)
	}
	if stored, err := sameNative.Message(ctx, original.ID); err != nil || !stored.Edited || stored.OriginalText != corrected || stored.ProjectionSourceID != domain.ID(latestID) {
		t.Fatalf("re-export lost the old native root's verified correction chain: %+v, %v", stored, err)
	}
}
