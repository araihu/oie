package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreAttachmentEchoRequiresUploadedWireBody(t *testing.T) {
	source, _, placeholder := outboxFixture(t)
	engine, room := promoteBackupRoomFixture(t, source, placeholder)
	engine.SetProtocols(&restoreAttachmentProtocol{})
	ctx := t.Context()
	staged, _, err := engine.StageAttachment(ctx, room.ID, migrationID(), "original.txt", "text/plain", strings.NewReader("attached file bytes"))
	if err != nil {
		t.Fatal(err)
	}
	const caption = "the approved caption"
	accepted, err := engine.SendComposition(ctx, migrationID(), room.ID, domain.OutgoingComposition{
		OriginalText: caption, AttachmentIDs: []domain.ID{staged.ID},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	if pending.MatchesWireBody(caption) {
		t.Fatal("the staged composition has no uploaded wire body yet")
	}
	dest := copyRestoreFixture(t, source)
	intent, err := source.queries.GetRoomIntent(ctx, string(room.ID))
	if err != nil {
		t.Fatal(err)
	}
	at := pending.CreatedAt.Add(time.Minute)
	claimed, err := source.ClaimDispatchMessage(ctx, pending.ID, pending.RevisionID, domain.ID(intent.RevisionID), migrationID(), at, true)
	if err != nil {
		t.Fatal(err)
	}
	const wire = caption + "\nhttps://example.invalid/upload/original.txt"
	if err := source.RecordOriginalWireAttempt(ctx, claimed.ID, claimed.RevisionID, application.Outgoing{OriginalText: caption}, wire); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, claimed.ID, "dispatching", "submitted", migrationID(), at, at); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, at); err != nil {
		t.Fatal(err)
	}

	receiver := testRestoreEngine(t, dest)
	receiver.SetProtocols(&outboundProtocol{})
	const captionArchive = "caption-only-archive"
	captionReference := domain.MessageReference{
		Scope: "room:" + room.Address, Author: room.Address + "/Tester", ID: "caption-only-stanza-id",
	}
	echo := application.Incoming{
		AccountID: room.AccountID, Address: room.Address, Kind: "room",
		IsOwn: true, Historical: true, SenderAddress: "alice@localhost", CreatedAt: at,
		CanonicalID: pending.ID, OriginalText: caption, ProviderID: "room:caption-only",
		ArchiveScope: room.Address, ArchiveID: captionArchive, References: []domain.MessageReference{captionReference},
	}
	if err := receiver.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	captionID, err := dest.read.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
		AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: captionArchive,
	})
	if err != nil || captionID == string(pending.ID) {
		t.Fatalf("caption-only echo was linked to a different attachment body: %s, %v", captionID, err)
	}
	stored, err := receiver.Message(ctx, pending.ID)
	if err != nil || len(stored.References) != 0 {
		t.Fatalf("caption-only stanza became a structured target for the attachment: %+v, %v", stored.References, err)
	}

	const wireArchive = "full-attachment-archive"
	echo.OriginalText, echo.ProviderID, echo.ArchiveID, echo.References = wire, "room:full-attachment", wireArchive, nil
	if err := receiver.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	wireID, err := dest.read.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
		AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: wireArchive,
	})
	if err != nil || wireID != string(pending.ID) {
		t.Fatalf("the exact uploaded body did not link to the restored attachment: %s, %v", wireID, err)
	}
	current, err := receiver.Message(ctx, pending.ID)
	if err != nil || current.RevisionID != stored.RevisionID || current.AttachmentWireText != wire || current.SendState != "submitted" {
		t.Fatalf("processing archive echoes changed the restored attachment: %+v, %v", current, err)
	}
}

func TestBackupRestoreCaptionOnlyEchoStopsResumedAttachmentWithoutLinking(t *testing.T) {
	source, _, placeholder := outboxFixture(t)
	engine, room := promoteBackupRoomFixture(t, source, placeholder)
	engine.SetProtocols(&restoreAttachmentProtocol{})
	dest := copyRestoreFixture(t, source)
	ctx := t.Context()
	staged, _, err := engine.StageAttachment(ctx, room.ID, migrationID(), "original.txt", "text/plain", strings.NewReader("attached file bytes"))
	if err != nil {
		t.Fatal(err)
	}
	const caption = "the approved caption"
	accepted, err := engine.SendComposition(ctx, migrationID(), room.ID, domain.OutgoingComposition{
		OriginalText: caption, AttachmentIDs: []domain.ID{staged.ID},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	at := pending.CreatedAt.Add(time.Minute)
	manifest, archive, scratch := backupRestoreFixture(t, source, true)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, at); err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, at); err != nil {
		t.Fatal(err)
	}
	const captionArchive = "caption-only-archive"
	receiver := testRestoreEngine(t, dest)
	receiver.SetProtocols(&outboundProtocol{})
	if err := receiver.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Address: room.Address, Kind: "room",
		IsOwn: true, Historical: true, SenderAddress: "alice@localhost", CreatedAt: at,
		CanonicalID: pending.ID, OriginalText: caption, ProviderID: "room:caption-only",
		ArchiveScope: room.Address, ArchiveID: captionArchive,
		References: []domain.MessageReference{{Scope: "room:" + room.Address, Author: room.Address + "/Tester", ID: "caption-only-stanza-id"}},
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := receiver.Message(ctx, pending.ID)
	if err != nil || stored.SendState != "unknown" || len(stored.References) != 0 {
		t.Fatalf("the observed origin should retire the send without linking a reply target: state=%s references=%+v error=%v", stored.SendState, stored.References, err)
	}
	outbox, err := dest.read.GetOutbox(ctx, string(pending.ID))
	if err != nil || outbox.State != "unknown" {
		t.Fatalf("the observed origin should stop dispatch: state=%s error=%v", outbox.State, err)
	}
	captionID, err := dest.read.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
		AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: captionArchive,
	})
	if err != nil || captionID == string(pending.ID) {
		t.Fatalf("caption-only echo should retain an independent identity: %s, %v", captionID, err)
	}
}
