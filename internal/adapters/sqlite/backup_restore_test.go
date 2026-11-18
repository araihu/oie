package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreMergesWithoutRevivingDeletedOrCancelledData(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	shared := replySeed(t, source, chat, "original", now.Add(-time.Hour), nil, nil)
	expired := replySeed(t, source, chat, "deleted from destination", now.AddDate(0, 0, -20), nil, nil)
	sent := queueBackupFixture(t, engine, chat.ID, "cancelled in the destination", nil)
	dest := copyRestoreFixture(t, source)
	defer func() { _ = dest.Close() }()

	older := addBackupMessageRevision(t, source, shared, "archived ancestor", now.Add(-time.Minute))
	newer := addBackupMessageRevision(t, dest, older, "destination descendant", now.Add(-time.Hour))
	destEngine := testRestoreEngine(t, dest)
	cancelled, err := destEngine.CancelMessage(ctx, migrationID(), sent.ID, sent.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	days := 7
	if _, err := destEngine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	count, err := dest.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil })
	if err != nil || count != 1 {
		t.Fatalf("expiry fixture: count=%d, error=%v", count, err)
	}
	existing := queueBackupFixture(t, destEngine, chat.ID, "already queued", nil)
	if err := destEngine.RemoveDirectFromChats(ctx, migrationID(), chat.ID, chat.AccountID, newerConversation(t, dest, chat.ID).RevisionID); err != nil {
		t.Fatal(err)
	}
	added := replySeed(t, source, chat, "recovered history", now, nil, nil)
	tooOld := replySeed(t, source, chat, "outside retention", now.AddDate(0, 0, -8), nil, nil)
	starred := replySeed(t, source, chat, "starred history", now.AddDate(0, 0, -10), nil, nil)
	if _, err := engine.SetMessageStar(ctx, migrationID(), starred.ID, "", true); err != nil {
		t.Fatal(err)
	}
	when := now.Add(time.Hour)
	recovered := queueBackupFixture(t, engine, chat.ID, "recovered schedule", &when)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Messages != 2 || result.PausedSends != 1 {
		t.Fatalf("unexpected merge result: %+v", result)
	}
	for _, id := range []domain.ID{added.ID, starred.ID} {
		if _, err := dest.Message(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []domain.ID{expired.ID, tooOld.ID, recovered.ID} {
		if _, err := dest.Message(ctx, id); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("%s became visible: %v", id, err)
		}
	}
	current, err := dest.Message(ctx, shared.ID)
	if err != nil || current.RevisionID != newer.RevisionID || current.OriginalText != "destination descendant" {
		t.Fatalf("current revision changed: %+v %v", current, err)
	}
	current, err = dest.Message(ctx, sent.ID)
	if err != nil || current.SendState != "cancelled" || current.RevisionID != cancelled.Message.RevisionID {
		t.Fatalf("cancellation was lost: %+v %v", current, err)
	}
	if !newerConversation(t, dest, chat.ID).RemovedFromChats {
		t.Fatal("restore reopened a removed chat")
	}
	if err := dest.Close(); err != nil {
		t.Fatal(err)
	}
	dest, err = Open(ctx, dest.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := dest.RecoveredSends(ctx)
	if err != nil || len(pending) != 1 || pending[0].MessageID != recovered.ID || !pending[0].ScheduledAt.Equal(when) {
		t.Fatalf("paused items after restart: %+v %v", pending, err)
	}
	dispatchable, err := dest.PendingMessages(ctx, now.Add(2*time.Hour))
	if err != nil || len(dispatchable) != 1 || dispatchable[0].ID != existing.ID {
		t.Fatalf("dispatchable after restart: %+v %v", dispatchable, err)
	}
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("same archive applied twice: %+v %v", result, err)
	}
	if err := dest.ReviewRecoveredSend(ctx, recovered.ID, recovered.RevisionID, false, now); err != nil {
		t.Fatal(err)
	}
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.PausedSends != 0 {
		t.Fatalf("discarded send was recovered: %+v %v", result, err)
	}
}

func TestBackupRestoreConcurrentRevisionsChooseSameWinner(t *testing.T) {
	t.Parallel()
	base, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	root := replySeed(t, base, chat, "common ancestor", now, nil, nil)
	left, right := copyRestoreFixture(t, base), copyRestoreFixture(t, base)
	l := addBackupMessageRevision(t, left, root, "left", now.Add(time.Minute))
	r := addBackupMessageRevision(t, right, root, "right", now.Add(time.Minute))
	leftManifest, leftDB, leftScratch := backupRestoreFixture(t, left, false)
	rightManifest, rightDB, rightScratch := backupRestoreFixture(t, right, false)
	if _, err := left.mergeBackup(ctx, rightDB, rightScratch, rightManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := right.mergeBackup(ctx, leftDB, leftScratch, leftManifest, now); err != nil {
		t.Fatal(err)
	}
	want := l
	if r.RevisionID > l.RevisionID {
		want = r
	}
	for _, store := range []*Store{left, right} {
		current, err := store.Message(ctx, root.ID)
		if err != nil || current.RevisionID != want.RevisionID {
			t.Fatalf("non-deterministic head: %+v %v", current, err)
		}
		for _, rev := range []domain.ID{l.RevisionID, r.RevisionID} {
			var n int
			if err := store.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM message_revisions WHERE id=?", rev).Scan(&n); err != nil || n != 1 {
				t.Fatalf("alternative lost: %s %d %v", rev, n, err)
			}
		}
	}
}

func TestBackupRestoreRequiresOwnerAndExplicitReviewForResuming(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	sameOwner := copyRestoreFixture(t, source)
	due := now.Add(time.Hour)
	key := migrationID()
	composition := domain.OutgoingComposition{OriginalText: "recoverable"}
	accepted, err := engine.SendComposition(ctx, key, chat.ID, composition, &due)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	uncertain := queueBackupFixture(t, engine, chat.ID, "uncertain", nil)
	if _, err := source.TransitionMessage(ctx, uncertain.ID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	unknown, err := source.TransitionMessage(ctx, uncertain.ID, "dispatching", "unknown", migrationID(), now, now)
	if err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	fresh, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	for _, dest := range []*Store{sameOwner, fresh} {
		if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.PausedSends != 2 {
			t.Fatalf("merge: %+v %v", result, err)
		}
		items, err := dest.PendingMessages(ctx, due.Add(time.Hour))
		if err != nil || len(items) != 0 {
			t.Fatalf("recovered items entered dispatch: %+v %v", items, err)
		}
	}
	if err := fresh.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, now); err == nil {
		t.Fatal("restored profile silently took ownership")
	}
	if err := sameOwner.ReviewRecoveredSend(ctx, unknown.ID, unknown.RevisionID, true, now); err == nil {
		t.Fatal("unknown outcome was retried")
	}
	if err := sameOwner.ReviewRecoveredSend(ctx, pending.ID, migrationID(), true, now); err == nil {
		t.Fatal("resume accepted a stale revision")
	}
	sameEngine := testRestoreEngine(t, sameOwner)
	if duplicate, err := sameEngine.SendComposition(ctx, key, chat.ID, composition, &due); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("retried operation bypassed the review queue: %+v %v", duplicate, err)
	}
	if err := sameOwner.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, now); err != nil {
		t.Fatal(err)
	}
	replayed, err := sameEngine.SendComposition(ctx, key, chat.ID, composition, &due)
	if err != nil || !replayed.Replayed || replayed.Message.ID != pending.ID {
		t.Fatalf("resume lost the original receipt: %+v %v", replayed, err)
	}
	items, err := sameOwner.PendingMessages(ctx, due.Add(time.Hour))
	if err != nil || len(items) != 1 || items[0].ID != pending.ID || !items[0].ScheduledAt.Equal(due) {
		t.Fatalf("explicit resume lost original identity or schedule: %+v %v", items, err)
	}
}

func copyRestoreFixture(t *testing.T, source *Store) *Store {
	t.Helper()
	dest := t.TempDir()
	filename := filepath.Join(dest, "oie.sqlite")
	if err := os.WriteFile(filename, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := copySQLiteBackup(t.Context(), source.readDB, filename); err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.Context(), dest, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func backupRestoreFixture(t *testing.T, source *Store, media bool) (backupManifest, string, string) {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "backup.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBackup(t.Context(), source.readDB, source.dir, file, media); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	manifest, err := readBackup(t.Context(), file.Name(), scratch)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, filepath.Join(scratch, backupDatabaseName), scratch
}

func testRestoreEngine(t *testing.T, store *Store) *application.Engine {
	t.Helper()
	engine, err := application.New(store, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func queueBackupFixture(t *testing.T, engine *application.Engine, chat domain.ID, text string, due *time.Time) domain.Message {
	t.Helper()
	result, err := engine.SendComposition(t.Context(), migrationID(), chat, domain.OutgoingComposition{OriginalText: text}, due)
	if err != nil {
		t.Fatal(err)
	}
	return result.Message
}

func addBackupMessageRevision(t *testing.T, store *Store, parent domain.Message, text string, at time.Time) domain.Message {
	t.Helper()
	ctx := t.Context()
	message := parent
	message.RevisionID, message.OriginalText = migrationID(), text
	tx, q, err := begin(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.InsertMessageRevision(ctx, dbgen.InsertMessageRevisionParams{
		ID: string(message.RevisionID), EntityID: string(message.ID), Parents: jsonText([]domain.ID{parent.RevisionID}), Payload: jsonText(message), CreatedAt: stamp(at),
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpdateMessage(ctx, dbgen.UpdateMessageParams{ID: string(message.ID), RevisionID: string(message.RevisionID), State: message.SendState, Payload: jsonText(message)}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return message
}

func newerConversation(t *testing.T, store *Store, id domain.ID) domain.Conversation {
	t.Helper()
	chat, err := store.Conversation(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return chat
}

type restoreAttachmentProtocol struct{ outboundProtocol }

func (*restoreAttachmentProtocol) AttachmentAvailability(context.Context, domain.Account, []domain.StagedAttachment) error {
	return nil
}

func TestBackupRestoreMediaAndPausedSendsSurviveAnotherBackup(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	engine.SetProtocols(&restoreAttachmentProtocol{})
	ctx := t.Context()
	now := time.Now().UTC()
	contents := "file referenced by the pending composition"
	attachment, _, err := engine.StageAttachment(ctx, chat.ID, migrationID(), "fixture.txt", "text/plain", strings.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	when := now.Add(time.Hour)
	result, err := engine.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{
		OriginalText: "with a file", AttachmentIDs: []domain.ID{attachment.ID},
	}, &when)
	if err != nil {
		t.Fatal(err)
	}
	second := queueBackupFixture(t, engine, chat.ID, "review and discard", nil)
	manifest, archive, scratch := backupRestoreFixture(t, source, true)
	first, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if merged, err := first.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || merged.MediaFiles != 1 {
		t.Fatalf("media merge: %+v %v", merged, err)
	}
	if err := first.ReviewRecoveredSend(ctx, second.ID, second.RevisionID, false, now); err != nil {
		t.Fatal(err)
	}
	rebackup, rearchive, rescratch := backupRestoreFixture(t, first, true)
	secondDestination, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secondDestination.Close() }()
	if merged, err := secondDestination.mergeBackup(ctx, rearchive, rescratch, rebackup, now); err != nil || merged.PausedSends != 1 || merged.MediaFiles != 1 {
		t.Fatalf("second merge: %+v %v", merged, err)
	}
	if merged, err := secondDestination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || merged.PausedSends != 0 {
		t.Fatalf("discarded send was restored: %+v %v", merged, err)
	}
	if items, err := secondDestination.RecoveredSends(ctx); err != nil || len(items) != 1 || items[0].MessageID != result.Message.ID || items[0].SourceEngineID != manifest.SourceEngine {
		t.Fatalf("paused composition or owner changed: %+v %v", items, err)
	}
	data, err := os.ReadFile(filepath.Join(secondDestination.dir, "media", "staged", string(attachment.ID)))
	if err != nil || string(data) != contents {
		t.Fatalf("staged attachment changed: %q %v", data, err)
	}
}

func TestBackupV1MatchesIndependentAccountsAndChats(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	destination, _, other := outboxFixture(t)
	now := time.Now().UTC()
	restored := replySeed(t, source, chat, "source history", now, nil, nil)
	local := replySeed(t, destination, other, "destination history", now, nil, nil)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	result, err := destination.mergeBackup(t.Context(), archive, scratch, manifest, now)
	if err != nil || result.Accounts != 0 || result.Conversations != 0 || result.Messages != 1 {
		t.Fatalf("merge: %+v %v", result, err)
	}
	for _, id := range []domain.ID{restored.ID, local.ID} {
		m, err := destination.Message(t.Context(), id)
		if err != nil || m.AccountID != other.AccountID || m.ConversationID != other.ID || m.Sender.ID != other.PeerID {
			t.Fatalf("cross-profile links: %+v %v", m, err)
		}
	}
}

func TestBackupRestoreLeavesRemovedAccountAndItsHistoryDeleted(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	replySeed(t, source, chat, "removed account history", now, nil, nil)
	queueBackupFixture(t, engine, chat.ID, "removed account send", nil)
	destination := copyRestoreFixture(t, source)
	if _, err := destination.RemoveAccount(ctx, chat.AccountID, now); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("removed account was reimported: %+v %v", result, err)
	}
	if _, err := destination.Account(ctx, chat.AccountID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("removed account was reimported: %v", err)
	}
}

func TestBackupRestoreKeepsCorrectionsAndDestinationRetraction(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	engine.SetProtocols(&outboundProtocol{})
	const scope = "xmpp:correct:sender:bob@localhost"
	const author = "bob@localhost"
	receiveCorrection(t, engine, chat, "original-id", "first", "", scope, author, now.Add(-time.Minute))
	canonical := correctionMessages(t, engine, chat)[0]
	destination := copyRestoreFixture(t, source)
	receiveCorrection(t, engine, chat, "edit-id", "second", "original-id", scope, author, now)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if err := destination.RecordMessageRetraction(ctx, chat.AccountID, chat.Kind, chat.Address, application.IncomingRetraction{
		Scope: scope, RemoteID: "original-id", At: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	current, err := destination.Message(ctx, canonical.ID)
	if err != nil || current.Retraction == nil || current.OriginalText != "" {
		t.Fatalf("destination retraction was lost: %+v %v", current, err)
	}
	aliases, err := destination.queries.ExpirySourceIDs(ctx, jsonText([]domain.ID{canonical.ID}))
	if err != nil || len(aliases) != 1 {
		t.Fatalf("correction graph was lost: %+v %v", aliases, err)
	}
}
