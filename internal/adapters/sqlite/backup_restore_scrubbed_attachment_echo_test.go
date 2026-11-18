package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRecognizesAttachmentEchoAfterScrubbingSenderOnlyQuote(t *testing.T) {
	t.Parallel()
	source, _, placeholder := outboxFixture(t)
	engine, room := promoteBackupRoomFixture(t, source, placeholder)
	ctx := t.Context()
	now := time.Now().UTC()
	const raw = "> a fallback-only inbound quote\n"
	const answer = "the authored answer"
	target := replySeed(t, source, room, raw, now.AddDate(0, 0, -20), nil, &domain.ReplyReference{
		Target: domain.MessageReference{ID: "unretained-peer-msg"}, Fallback: []domain.TextRange{{End: len(raw)}},
	})
	dest := copyRestoreFixture(t, source)
	engine.SetProtocols(&restoreAttachmentProtocol{})
	staged, _, err := engine.StageAttachment(ctx, room.ID, migrationID(), "file.txt", "text/plain", strings.NewReader("text file"))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := engine.SendComposition(ctx, migrationID(), room.ID, domain.OutgoingComposition{
		OriginalText: answer, ReplyTo: target.ID, AttachmentIDs: []domain.ID{staged.ID},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	if plan := pending.OutboundReply; plan == nil || plan.Excerpt != "" ||
		plan.SenderName == "" || plan.Reference != nil || pending.OriginalText == answer {
		t.Fatalf("fixture did not create a sender-only quote: %+v", pending)
	}

	manifest, archive, scratch := backupRestoreFixture(t, source, true)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	receiver := testRestoreEngine(t, dest)
	receiver.SetProtocols(&outboundProtocol{})
	intent, err := dest.queries.GetRoomIntent(ctx, string(room.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, now); err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Minute)
	claim, err := dest.ClaimDispatchMessage(ctx, pending.ID, pending.RevisionID, domain.ID(intent.RevisionID), migrationID(), at, true)
	if err != nil {
		t.Fatal(err)
	}
	const suffix = "\nhttps://example.invalid/upload/file.txt"
	wire := pending.OriginalText + suffix
	if err := dest.RecordOriginalWireAttempt(ctx, claim.ID, claim.RevisionID, application.Outgoing{OriginalText: pending.OriginalText}, wire); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.TransitionMessage(ctx, pending.ID, "dispatching", "submitted", migrationID(), at, at); err != nil {
		t.Fatal(err)
	}
	for range domain.RoomRetentionMinimum {
		replySeed(t, dest, room, "new retained history", now, nil, nil)
	}
	days := 1
	if _, err := receiver.SetConversationRetention(ctx, room.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if n, err := dest.PurgeExpiredHistory(ctx, at, func() (domain.ID, error) { return migrationID(), nil }); err != nil || n != 1 {
		t.Fatalf("target purge: %d, %v", n, err)
	}
	scrubbed, err := dest.Message(ctx, pending.ID)
	if err != nil || scrubbed.OriginalText != answer || scrubbed.AttachmentWireText != answer+suffix {
		t.Fatalf("quote scrub did not retain the answer and uploaded URL: %+v, %v", scrubbed, err)
	}
	if scrubbed.MatchesWireBody(pending.OriginalText + "\nhttps://example.invalid/upload/other.txt") {
		t.Fatal("a different uploaded URL matched the retained wire body")
	}
	if scrubbed.MatchesAttachmentWireBody(pending.OriginalText) {
		t.Fatal("the caption alone matched the uploaded attachment wire body")
	}

	const echoID = "own-attachment-echo-without-reply-reference"
	if err := receiver.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		SenderAddress: "alice@localhost", SenderName: "Tester", IsOwn: true,
		OriginalText: wire, Historical: true, CreatedAt: at, CanonicalID: pending.ID,
		ProviderID: "room:archived-sender-only-echo", ArchiveScope: room.Address, ArchiveID: echoID,
	}); err != nil {
		t.Fatal(err)
	}
	id, err := dest.read.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
		AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: echoID,
	})
	if err != nil || id != string(pending.ID) {
		t.Fatalf("own echo created another logical message: %s, %v", id, err)
	}
	current, err := dest.Message(ctx, pending.ID)
	if err != nil || current.RevisionID != scrubbed.RevisionID || current.OriginalText != answer ||
		current.AttachmentWireText != answer+suffix {
		t.Fatalf("own echo restored the scrubbed quote: %+v, %v", current, err)
	}
}
