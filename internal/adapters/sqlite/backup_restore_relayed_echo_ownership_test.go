package sqlite

import (
	"slices"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRelayedWitnessPreservesScopedReplyOwner(t *testing.T) {
	t.Parallel()
	source, _, placeholder := outboxFixture(t)
	sender, room := promoteBackupRoomFixture(t, source, placeholder)
	witness, relay := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	dest := copyRestoreFixture(t, source)
	ctx := t.Context()
	pending := queueBackupFixture(t, sender, room.ID, "message relayed from its original sender", nil)
	at := pending.CreatedAt.Add(time.Minute)
	if _, err := source.TransitionMessage(ctx, pending.ID, "queued", "dispatching", migrationID(), at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, pending.ID, "dispatching", "submitted", migrationID(), at, at); err != nil {
		t.Fatal(err)
	}
	contested := domain.MessageReference{Scope: "room:" + room.Address, Author: room.Address + "/Tester", ID: "occupied-stanza"}
	available := domain.MessageReference{Scope: contested.Scope, Author: contested.Author, ID: "independent-stanza"}
	echo := application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address, IsOwn: true, SenderAddress: "alice@localhost",
		CanonicalID: pending.ID, ProviderID: "room:independent-witness", Historical: true, CreatedAt: at,
		ArchiveScope: room.Address, ArchiveID: "archived-competing-packet", OriginalText: pending.OriginalText,
		References: []domain.MessageReference{contested},
	}
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	sender.SetProtocols(&outboundProtocol{})
	for _, engine := range []*application.Engine{witnessEngine, sender} {
		if err := engine.Receive(ctx, echo); err != nil {
			t.Fatal(err)
		}
	}
	other := echo
	other.ArchiveID, other.References = "independent-archive-key", []domain.MessageReference{available}
	if err := witnessEngine.Receive(ctx, other); err != nil {
		t.Fatal(err)
	}
	witnesses, err := witness.Messages(ctx, room.ID, "", 10)
	if err != nil || len(witnesses) != 1 || witnesses[0].ID == pending.ID {
		t.Fatalf("expected the independent witness: %+v, %v", witnesses, err)
	}
	witnessed := witnesses[0]
	witnessReply := replySeed(t, witness, room, "the reply with an independent stanza ID", at.Add(time.Second), nil, &domain.ReplyReference{Target: available})
	for _, store := range []*Store{witness, source} {
		manifest, archive, scratch := backupRestoreFixture(t, store, false)
		if _, err := relay.mergeBackup(ctx, archive, scratch, manifest, at); err != nil {
			t.Fatal(err)
		}
	}
	if m, err := relay.Message(ctx, pending.ID); err != nil || !slices.Contains(m.References, contested) || !slices.Contains(m.References, available) {
		t.Fatalf("fixture did not retain both witnessed references: %+v, %v", m.References, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, relay, false)

	destEngine := testRestoreEngine(t, dest)
	destEngine.SetProtocols(&outboundProtocol{})
	local := echo
	local.IsOwn, local.CanonicalID, local.SenderAddress = false, "", "charlie@localhost"
	local.ProviderID, local.OriginalText = "room:local-reference-owner", "the existing destination target"
	if err := destEngine.Receive(ctx, local); err != nil {
		t.Fatal(err)
	}
	archiveAlias := func(archiveID string) (string, error) {
		return dest.read.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: archiveID})
	}
	ownerID, err := archiveAlias(local.ArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	localReply := replySeed(t, dest, room, "the existing reply", at.Add(2*time.Second), nil, &domain.ReplyReference{Target: contested})
	assertReply := func(id, owner domain.ID, excerpt string) {
		t.Helper()
		m, err := dest.Message(ctx, id)
		if err != nil || m.Reply == nil || !m.Reply.Available || m.Reply.MessageID != owner || m.Reply.Excerpt != excerpt {
			t.Fatalf("reply did not retain the expected owner %s: %+v, %v", owner, m.Reply, err)
		}
	}
	assertReply(localReply.ID, domain.ID(ownerID), local.OriginalText)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertReply(localReply.ID, domain.ID(ownerID), local.OriginalText)
	assertReply(witnessReply.ID, pending.ID, pending.OriginalText)
	if m, err := dest.Message(ctx, pending.ID); err != nil || !slices.Equal(m.References, []domain.MessageReference{available}) {
		t.Fatalf("restored send acquired a competing reference: %+v, %v", m.References, err)
	}
	if m, err := dest.MessageSource(ctx, witnessed.ID); err != nil || m.RevisionID != witnessed.RevisionID {
		t.Fatalf("restored witness revision did not survive: %+v, %v", m, err)
	}
	if got, err := archiveAlias(local.ArchiveID); err != nil || got != ownerID {
		t.Fatalf("archive key moved from existing target %q to %q: %v", ownerID, got, err)
	}
	if got, err := archiveAlias(other.ArchiveID); err != nil || got != string(witnessed.ID) {
		t.Fatalf("independent archive key was lost: %q, %v", got, err)
	}
	_, before, err := dest.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, at.Add(time.Hour)); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("repeat restore changed relay evidence: %+v, %v", result, err)
	}
	if _, after, err := dest.Snapshot(ctx); err != nil || after != before {
		t.Fatalf("repeat restore emitted events from %d to %d: %v", before, after, err)
	}
	assertReply(localReply.ID, domain.ID(ownerID), local.OriginalText)
	assertReply(witnessReply.ID, pending.ID, pending.OriginalText)
}
