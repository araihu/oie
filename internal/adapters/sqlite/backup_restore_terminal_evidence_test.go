package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsScheduledPlacementAfterLateRejection(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	for _, state := range []string{"submitted", "failed"} {
		t.Run(state, func(t *testing.T) {
			testBackupRestoreKeepsAttachmentWireBodyForArchiveEcho(t, state)
		})
	}
}

func testBackupRestoreKeepsAttachmentWireBodyForArchiveEcho(t *testing.T, state string) {
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
	wantImportedWire := archivedWire
	if state == "failed" {
		if err := engine.Reject(ctx, application.Rejected{AccountID: room.AccountID, MessageID: string(pending.ID), FromServer: true}); err != nil {
			t.Fatal(err)
		}
		wantImportedWire = ""
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
		{store: destination, wire: wantImportedWire},
		{store: withWire, wire: localWire},
	} {
		if _, err := tc.store.mergeBackup(ctx, archive, scratch, manifest, sentAt.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		prior, err := tc.store.Message(ctx, pending.ID)
		if err != nil || prior.SendState != state || prior.AttachmentWireText != tc.wire {
			t.Fatalf("restore changed the expected state or wire body: %+v, %v", prior, err)
		}
		receiver := testRestoreEngine(t, tc.store)
		receiver.SetProtocols(&outboundProtocol{})
		echo := application.Incoming{
			AccountID: room.AccountID, Address: room.Address, Kind: "room", Historical: true, IsOwn: true,
			SenderAddress: "alice@localhost", CreatedAt: sentAt, OriginalText: archivedWire,
			CanonicalID: pending.ID, ProviderID: "room:authenticated-own-attachment",
			ArchiveScope: room.Address, ArchiveID: "own-attachment",
		}
		if err := receiver.Receive(ctx, echo); err != nil {
			t.Fatal(err)
		}
		items, err := tc.store.Messages(ctx, room.ID, "", 100)
		if err != nil || len(items) != 1 || items[0].ID != pending.ID {
			t.Fatalf("own archive replay duplicated the restored send: %+v, %v", items, err)
		}
		if got := items[0]; got.RevisionID != prior.RevisionID || got.OriginalText != prior.OriginalText ||
			got.AttachmentWireText != prior.AttachmentWireText || got.SendState != prior.SendState {
			t.Fatalf("own archive replay changed the retained message: before %+v, after %+v", prior, got)
		}
		alias, err := tc.store.queries.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: echo.ArchiveID})
		if err != nil || alias != string(pending.ID) {
			t.Fatalf("archive ID points to %q, want %q, error %v", alias, pending.ID, err)
		}
		for _, unverified := range []application.Incoming{
			{ProviderID: "room:other-body", ArchiveID: "other-body", IsOwn: true, OriginalText: "another upload"},
			{ProviderID: "room:other-author", ArchiveID: "other-author", OriginalText: archivedWire, SenderAddress: "charlie@localhost"},
			{ProviderID: "room:other-room", ArchiveID: "other-room", IsOwn: true, OriginalText: archivedWire, Address: "another-room@localhost"},
		} {
			if unverified.Address == "" {
				unverified.Address = echo.Address
			}
			unverified.AccountID, unverified.Kind = echo.AccountID, echo.Kind
			unverified.CreatedAt, unverified.Historical = echo.CreatedAt, echo.Historical
			unverified.CanonicalID, unverified.ArchiveScope = echo.CanonicalID, unverified.Address
			if err := receiver.Receive(ctx, unverified); err != nil {
				t.Fatalf("receive unmatched archive message %q: %v", unverified.ArchiveID, err)
			}
			alias, err := tc.store.queries.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{AccountID: string(room.AccountID), Scope: unverified.ArchiveScope, ArchiveID: unverified.ArchiveID})
			if err != nil || alias == string(pending.ID) {
				t.Fatalf("unmatched archive message %q points to %q, error %v", unverified.ArchiveID, alias, err)
			}
		}
	}
}
