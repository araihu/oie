package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsScheduledPlacementAfterLateRejection(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	scheduled := queueBackupFixture(t, engine, chat.ID, "the historical scheduled send", &due)
	destination := copyRestoreFixture(t, source)
	sentAt, localAt, rejectedAt := due.Add(time.Minute), due.Add(2*time.Minute), due.Add(3*time.Minute)
	newer := replySeed(t, destination, chat, "newer destination activity", localAt, nil, nil)
	if _, err := source.TransitionMessage(ctx, scheduled.ID, "queued", "dispatching", migrationID(), sentAt, sentAt); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, scheduled.ID, "dispatching", "submitted", migrationID(), sentAt, sentAt); err != nil {
		t.Fatal(err)
	}
	providerHost, err := application.New(source, "local", func() (domain.ID, error) { return migrationID(), nil }, func() time.Time { return rejectedAt })
	if err != nil {
		t.Fatal(err)
	}
	if err := providerHost.Reject(ctx, application.Rejected{AccountID: chat.AccountID, MessageID: string(scheduled.ID), FromServer: true}); err != nil {
		t.Fatal(err)
	}
	known, err := source.Message(ctx, scheduled.ID)
	if err != nil || known.SendState != "failed" || !known.TimelineTime().Equal(sentAt) {
		t.Fatalf("source lost the historical placement after rejection: %+v, %v", known, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	_, sequence, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, rejectedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := destination.Message(ctx, scheduled.ID)
	if err != nil || got.SendState != "failed" || !got.TimelineTime().Equal(sentAt) {
		t.Fatalf("restore moved the send to its rejection time: %+v, %v", got, err)
	}
	row, err := destination.read.GetMessage(ctx, string(scheduled.ID))
	if err != nil || row.SubmittedAtMs == nil || *row.SubmittedAtMs != sentAt.UnixMilli() {
		t.Fatalf("restore lost the actual send time used for retention: %+v, %v", row.SubmittedAtMs, err)
	}
	if current := newerConversation(t, destination, chat.ID); current.LastMessage != newer.OriginalText {
		t.Fatalf("historical rejection took precedence over the newer local message: %+v", current)
	}
	events, err := destination.Events(ctx, sequence, 100)
	if err != nil || len(events) == 0 || events[0].Message == nil || events[0].Message.ID != scheduled.ID ||
		events[0].Message.SendState != "failed" || !events[0].OccurredAt.Equal(rejectedAt) {
		t.Fatalf("terminal state event did not record the rejection time: %+v, %v", events, err)
	}
}

func TestBackupRestoreKeepsSubmittedAttachmentWireBodyForArchiveEcho(t *testing.T) {
	source, _, placeholder := outboxFixture(t)
	engine, room := promoteBackupRoomFixture(t, source, placeholder)
	engine.SetProtocols(&restoreAttachmentProtocol{})
	ctx := t.Context()
	staged, _, err := engine.StageAttachment(ctx, room.ID, migrationID(), "original.txt", "text/plain", strings.NewReader("some file bytes"))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := engine.SendComposition(ctx, migrationID(), room.ID, domain.OutgoingComposition{
		OriginalText: "the attachment", AttachmentIDs: []domain.ID{staged.ID},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	destination, withWire := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	sentAt := pending.CreatedAt.Add(2 * time.Minute)
	archivedWire := pending.OriginalText + "\nhttps://example.invalid/upload/original.txt"
	localWire := pending.OriginalText + "\nhttps://example.invalid/upload/local-attempt.txt"
	recordWire := func(store *Store, at time.Time, wire string) {
		t.Helper()
		intent, err := store.queries.GetRoomIntent(ctx, string(room.ID))
		if err != nil {
			t.Fatal(err)
		}
		claim, err := store.ClaimDispatchMessage(ctx, pending.ID, pending.RevisionID, domain.ID(intent.RevisionID), migrationID(), at, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordOriginalWireAttempt(ctx, claim.ID, claim.RevisionID, application.Outgoing{OriginalText: claim.OriginalText}, wire); err != nil {
			t.Fatal(err)
		}
	}
	recordWire(source, sentAt, archivedWire)
	if _, err := source.TransitionMessage(ctx, pending.ID, "dispatching", "submitted", migrationID(), sentAt, sentAt); err != nil {
		t.Fatal(err)
	}
	localAt := sentAt.Add(-time.Minute)
	recordWire(withWire, localAt, localWire)
	if _, err := withWire.TransitionMessage(ctx, pending.ID, "dispatching", "queued", migrationID(), localAt, localAt); err != nil {
		t.Fatal(err)
	}

	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	for _, tc := range []struct {
		store *Store
		wire  string
	}{
		{store: destination, wire: archivedWire},
		{store: withWire, wire: localWire},
	} {
		if _, err := tc.store.mergeBackup(ctx, archive, scratch, manifest, sentAt.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		prior, err := tc.store.Message(ctx, pending.ID)
		if err != nil || prior.SendState != "submitted" || prior.AttachmentWireText != tc.wire {
			t.Fatalf("restore lost the authenticated wire body: %+v, %v", prior, err)
		}
	}
	receiver := testRestoreEngine(t, destination)
	receiver.SetProtocols(&outboundProtocol{})
	if err := receiver.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Address: room.Address, Kind: "room", Historical: true, IsOwn: true,
		SenderAddress: "alice@localhost", CreatedAt: sentAt, OriginalText: archivedWire,
		CanonicalID: pending.ID, ProviderID: "room:authenticated-own-attachment",
		ArchiveScope: room.Address, ArchiveID: "own-attachment",
	}); err != nil {
		t.Fatal(err)
	}
	items, err := destination.Messages(ctx, room.ID, "", 100)
	if err != nil || len(items) != 1 || items[0].ID != pending.ID {
		t.Fatalf("own archive replay duplicated the restored submission: %+v, %v", items, err)
	}
}
