package sqlite

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsRepliesToVerifiedOwnEcho(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		attachment      bool
		alternateUpload bool
	}{
		{name: "plain text"},
		{name: "current attachment upload", attachment: true},
		{name: "previous attachment upload", attachment: true, alternateUpload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testBackupRestoreKeepsRepliesToVerifiedOwnEcho(t, tc.attachment, tc.alternateUpload)
		})
	}
}

func testBackupRestoreKeepsRepliesToVerifiedOwnEcho(t *testing.T, attachment, alternateUpload bool) {
	destination, _, placeholder := outboxFixture(t)
	destEngine, room := promoteBackupRoomFixture(t, destination, placeholder)
	witness := copyRestoreFixture(t, destination)
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	const caption = "our own earlier message"
	wireBody, unmatchedBody := caption, "different wire body"
	composition := domain.OutgoingComposition{OriginalText: caption}
	if attachment {
		destEngine.SetProtocols(&restoreAttachmentProtocol{})
		staged, _, err := destEngine.StageAttachment(ctx, room.ID, migrationID(), "original.txt", "text/plain", strings.NewReader("attached file bytes"))
		if err != nil {
			t.Fatal(err)
		}
		composition.AttachmentIDs = []domain.ID{staged.ID}
		wireBody = caption + "\nhttps://example.invalid/upload/original.txt"
		unmatchedBody = caption
	}
	accepted, err := destEngine.SendComposition(ctx, migrationID(), room.ID, composition, nil)
	if err != nil {
		t.Fatal(err)
	}
	destEngine.SetProtocols(&outboundProtocol{})
	pending := accepted.Message
	at := pending.CreatedAt.Add(time.Minute)
	finishAttempt := func(store *Store, wire, state string) {
		t.Helper()
		intent, err := store.queries.GetRoomIntent(ctx, string(room.ID))
		if err != nil {
			t.Fatal(err)
		}
		claim, err := store.ClaimDispatchMessage(ctx, pending.ID, pending.RevisionID, domain.ID(intent.RevisionID), migrationID(), pending.CreatedAt, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordOriginalWireAttempt(ctx, claim.ID, claim.RevisionID, application.Outgoing{OriginalText: caption}, wire); err != nil {
			t.Fatal(err)
		}
		if _, err := store.TransitionMessage(ctx, pending.ID, "dispatching", state, migrationID(), pending.CreatedAt, pending.CreatedAt); err != nil {
			t.Fatal(err)
		}
	}
	retainedWire := ""
	if attachment {
		retainedWire = wireBody
	}
	if alternateUpload {
		fork := copyRestoreFixture(t, destination)
		finishAttempt(fork, wireBody, "submitted")
		retainedWire = caption + "\nhttps://example.invalid/upload/destination-retry.txt"
		finishAttempt(destination, retainedWire, "queued")
		manifest, archive, scratch := backupRestoreFixture(t, fork, false)
		if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, at); err != nil {
			t.Fatal(err)
		}
	} else {
		finishAttempt(destination, retainedWire, "submitted")
	}
	local, err := destination.Message(ctx, pending.ID)
	if err != nil || local.SendState != "submitted" || local.AttachmentWireText != retainedWire {
		t.Fatalf("did not prepare the destination's retained send: %+v, %v", local, err)
	}
	ref := domain.MessageReference{Scope: "room:" + room.Address, Author: room.Address + "/Tester", ID: "room-assigned-id"}
	if err := witnessEngine.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		IsOwn: true, SenderAddress: "alice@localhost", SenderName: "Tester", ProviderID: "room:authenticated-provider",
		CanonicalID: pending.ID, ArchiveID: "own-echo", ArchiveScope: room.Address,
		OriginalText: wireBody, CreatedAt: pending.CreatedAt, Historical: true,
		References: []domain.MessageReference{ref},
	}); err != nil {
		t.Fatal(err)
	}
	if attachment {
		// A release which hashed the complete upload may already have indexed
		// this reference against the retained caption using the wrong hash.
		if _, err := destination.queries.InsertMessageReference(ctx, dbgen.InsertMessageReferenceParams{
			MessageID: string(local.ID), Scope: ref.Scope, RemoteID: ref.ID, Author: ref.Author, SourceHash: bodyHash(wireBody),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// An origin alone cannot rebind metadata for a different wire body.
	if err := witnessEngine.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		IsOwn: true, SenderAddress: "alice@localhost", ProviderID: "room:unmatched-provider",
		CanonicalID: pending.ID, ArchiveID: "unmatched-echo", ArchiveScope: room.Address,
		OriginalText: unmatchedBody, CreatedAt: pending.CreatedAt, Historical: true,
	}); err != nil {
		t.Fatal(err)
	}
	// A previously retained archive alias remains authoritative for its key.
	if err := destEngine.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		ProviderID: "room:local", ArchiveID: "occupied-alias", ArchiveScope: room.Address,
		OriginalText: "unrelated local message", CreatedAt: at, Historical: true,
	}); err != nil {
		t.Fatal(err)
	}
	getAlias := func(id string) (string, error) {
		return destination.queries.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
			AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: id,
		})
	}
	occupiedAlias, err := getAlias("occupied-alias")
	if err != nil {
		t.Fatal(err)
	}
	if err := witnessEngine.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		IsOwn: true, SenderAddress: "alice@localhost", ProviderID: "room:authenticated-provider",
		CanonicalID: pending.ID, ArchiveID: "occupied-alias", ArchiveScope: room.Address,
		OriginalText: wireBody, CreatedAt: pending.CreatedAt, Historical: true,
	}); err != nil {
		t.Fatal(err)
	}
	quote := "> " + pending.OriginalText + "\n"
	reply := replySeed(t, witness, room, quote+"hello from another sender", at, nil, &domain.ReplyReference{
		Target: ref, Fallback: []domain.TextRange{{Start: 0, End: len(quote)}},
	})
	sourceReply, err := witness.Message(ctx, reply.ID)
	if err != nil || sourceReply.Reply == nil || !sourceReply.Reply.Available {
		t.Fatalf("witness reply doesn't resolve: %+v, %v", sourceReply, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, witness, false)
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, at); err != nil || result != (RestoreSummary{Messages: 1}) {
		t.Fatalf("restore should add only the reply: %+v, %v", result, err)
	}
	own, err := destination.Message(ctx, pending.ID)
	if err != nil || own.SendState != "submitted" || own.RevisionID != local.RevisionID || own.AttachmentWireText != retainedWire {
		t.Fatalf("destination didn't keep own message: %+v, %v", own, err)
	}
	merged, err := destination.Message(ctx, reply.ID)
	if err != nil || merged.Reply == nil || !merged.Reply.Available || merged.Reply.OriginalExpired ||
		merged.Reply.MessageID != pending.ID || merged.OriginalText != reply.OriginalText {
		t.Fatalf("restore broke reply to retained original %s: %+v, %+v, %v", pending.ID, merged, merged.Reply, err)
	}
	if alias, err := getAlias("own-echo"); err != nil || alias != string(pending.ID) {
		t.Fatalf("verified echo alias points to %q, want %q, error %v", alias, pending.ID, err)
	}
	if alias, err := getAlias("unmatched-echo"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unmatched echo acquired an archive alias %q, error %v", alias, err)
	}
	if alias, err := getAlias("occupied-alias"); err != nil || alias != occupiedAlias {
		t.Fatalf("restore changed the existing archive alias from %q to %q, error %v", occupiedAlias, alias, err)
	}
	if expired, err := expiredReplyReference(ctx, destination.queries, room.ID, ref); err != nil || expired {
		t.Fatalf("represented reference was discarded as expired: %v, %v", expired, err)
	}
	_, before, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, at.Add(time.Minute)); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("reapplying the backup changed the destination: %+v, %v", result, err)
	}
	if _, after, err := destination.Snapshot(ctx); err != nil || before != after {
		t.Fatalf("reapplying the same echo emitted events from %d to %d, error %v", before, after, err)
	}
	if attachment {
		testVerifiedAttachmentEchoReferenceRepair(t, destination, destEngine, room, local, wireBody, at, alternateUpload)
	}
}

func testVerifiedAttachmentEchoReferenceRepair(t *testing.T, destination *Store, engine *application.Engine, room domain.Conversation, local domain.Message, wireBody string, at time.Time, historical bool) {
	t.Helper()
	ctx := t.Context()
	ref := domain.MessageReference{Scope: "room:" + room.Address, Author: room.Address + "/Tester", ID: "new-verified-stanza"}
	if _, err := destination.queries.InsertMessageReference(ctx, dbgen.InsertMessageReferenceParams{
		MessageID: string(local.ID), Scope: ref.Scope, RemoteID: ref.ID, Author: ref.Author, SourceHash: bodyHash(wireBody),
	}); err != nil {
		t.Fatal(err)
	}
	echo := application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		IsOwn: true, SenderAddress: "alice@localhost", ProviderID: "room:new-verified-provider", CanonicalID: local.ID,
		OriginalText: wireBody, CreatedAt: at, Historical: historical,
		References: []domain.MessageReference{ref},
	}
	if historical {
		echo.ArchiveID, echo.ArchiveScope = "new-verified-stanza-archive", room.Address
	}
	if err := engine.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	message, err := engine.Message(ctx, local.ID)
	if err != nil || message.RevisionID != local.RevisionID || !slices.Contains(message.References, ref) {
		t.Fatalf("verified upload echo did not restore its usable stanza reference: %+v, %v", message, err)
	}
	answer := replySeed(t, destination, room, "a reply to the uploaded attachment", at, nil, &domain.ReplyReference{Target: ref})
	got, err := engine.Message(ctx, answer.ID)
	if err != nil || got.Reply == nil || !got.Reply.Available || got.Reply.MessageID != local.ID {
		t.Fatalf("reply did not resolve to the uploaded attachment: %+v, %v", got.Reply, err)
	}
}
