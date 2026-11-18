package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreTerminalAfterWitnessEcho(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"room", "direct"} {
		t.Run(kind, func(t *testing.T) { testRestoreTerminalAfterWitnessEcho(t, kind) })
	}
}

func testRestoreTerminalAfterWitnessEcho(t *testing.T, kind string) {
	source, sender, chat := outboxFixture(t)
	if kind == "room" {
		sender, chat = promoteBackupRoomFixture(t, source, chat)
	}
	witness, dest, fresh := copyRestoreFixture(t, source), copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	destEngine := testRestoreEngine(t, dest)
	destEngine.SetProtocols(&outboundProtocol{})
	ctx := t.Context()

	key := migrationID()
	composition := domain.OutgoingComposition{OriginalText: "the terminal send from our profile"}
	accepted, err := sender.SendComposition(ctx, key, chat.ID, composition, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	at := pending.CreatedAt.Add(time.Minute)
	if _, err := source.TransitionMessage(ctx, pending.ID, "queued", "dispatching", migrationID(), at, at); err != nil {
		t.Fatal(err)
	}
	terminal, err := source.TransitionMessage(ctx, pending.ID, "dispatching", "submitted", migrationID(), at, at)
	if err != nil {
		t.Fatal(err)
	}
	provider, archiveScope := string(pending.ID), "alice@localhost"
	correction := domain.MessageCorrection{Scope: "xmpp:correct:sender:alice@localhost", Author: "alice@localhost", ID: string(pending.ID)}
	ref := domain.MessageReference{ID: string(pending.ID), Scope: "sender:alice@localhost", Author: "alice@localhost"}
	if kind == "room" {
		provider, archiveScope = "room:verified-provider-id", chat.Address
		ref = domain.MessageReference{ID: "server-stanza-id", Scope: "room:" + chat.Address, Author: chat.Address + "/Tester"}
		correction.Scope, correction.Author = "xmpp:correct:room:"+chat.Address+":verified-occupant", ref.Author
	}
	echo := application.Incoming{
		AccountID: chat.AccountID, Kind: kind, Address: chat.Address,
		CanonicalID: pending.ID, ProviderID: provider, IsOwn: true, SenderAddress: "alice@localhost",
		ArchiveID: "verified-mam-copy", ArchiveScope: archiveScope, OriginalText: pending.OriginalText,
		Historical: true, CreatedAt: at, References: []domain.MessageReference{ref}, Correction: &correction,
	}
	var differentBody *Store
	if kind == "direct" {
		differentBody = copyRestoreFixture(t, witness)
		engine := testRestoreEngine(t, differentBody)
		engine.SetProtocols(&outboundProtocol{})
		unmatched := echo
		unmatched.OriginalText = "a different body on the same origin"
		if err := engine.Receive(ctx, unmatched); err != nil {
			t.Fatal(err)
		}
	}
	if err := witnessEngine.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	witnessed, err := witness.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(witnessed) != 1 || witnessed[0].ID == pending.ID {
		t.Fatalf("witness has no independent archive echo: %+v %v", witnessed, err)
	}
	witnessID, witnessRevision := witnessed[0].ID, witnessed[0].RevisionID
	answer := replySeed(t, witness, chat, "the reply using the witness's protocol reference", at.Add(time.Second), nil, &domain.ReplyReference{Target: ref})
	firstBody, correctionCount := composition.OriginalText, 2
	if kind == "room" {
		// Both backups may contain the exact same scoped archive alias.
		sender.SetProtocols(&outboundProtocol{})
		if err := sender.Receive(ctx, echo); err != nil {
			t.Fatal(err)
		}
	} else {
		firstBody, correctionCount = "the witnessed correction before the merge", 3
		prior := correction
		prior.ID, prior.TargetID = "earlier-edit", correction.ID
		if err := witnessEngine.Receive(ctx, application.Incoming{
			AccountID: chat.AccountID, Kind: kind, Address: chat.Address,
			ProviderID: "earlier-edit", IsOwn: true, SenderAddress: "alice@localhost", Historical: true,
			CreatedAt: at.Add(1500 * time.Millisecond), OriginalText: firstBody, Correction: &prior,
		}); err != nil {
			t.Fatal(err)
		}
	}

	earlier, earlierDB, earlierScratch := backupRestoreFixture(t, witness, false)
	later, laterDB, laterScratch := backupRestoreFixture(t, source, false)
	if differentBody != nil {
		if _, err := differentBody.mergeBackup(ctx, laterDB, laterScratch, later, at.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
			t.Fatalf("an origin with a different body incorrectly waived the conflict: %v", err)
		}
	}
	if _, err := dest.mergeBackup(ctx, earlierDB, earlierScratch, earlier, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(ctx, laterDB, laterScratch, later, at.Add(time.Hour)); err != nil {
		t.Fatalf("could not merge the terminal send after its verified archive echo: %v", err)
	}
	assertHistory := func(store *Store, body string, aliasID domain.ID) {
		t.Helper()
		history, err := store.Messages(ctx, chat.ID, "", 10)
		if err != nil || len(history) != 2 || history[0].ID != answer.ID || history[1].ID != pending.ID ||
			history[1].OriginalText != body || !history[1].CreatedAt.Equal(pending.CreatedAt) {
			t.Fatalf("wrong logical messages or timeline after witness merge: %+v %v", history, err)
		}
		reply, err := store.Message(ctx, answer.ID)
		if err != nil || reply.Reply == nil || !reply.Reply.Available || reply.Reply.MessageID != pending.ID {
			t.Fatalf("reply lost its verified target: %+v, %v", reply.Reply, err)
		}
		native, err := store.MessageSource(ctx, pending.ID)
		if err != nil || native.RevisionID != terminal.RevisionID || native.ProviderID != string(pending.ID) {
			t.Fatalf("native identity or revision changed: %+v, %v", native, err)
		}
		witnessRow, err := store.MessageSource(ctx, witnessID)
		if err != nil || witnessRow.RevisionID != witnessRevision {
			t.Fatalf("witness revision was lost: %+v, %v", witnessRow, err)
		}
		alias, err := store.queries.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{
			AccountID: string(chat.AccountID), Scope: archiveScope, ArchiveID: echo.ArchiveID,
		})
		if err != nil || alias != string(aliasID) {
			t.Fatalf("stored witness archive alias changed to %q: %v", alias, err)
		}
	}
	assertHistory(dest, firstBody, witnessID)
	if kind == "direct" {
		t.Run("native_then_witness", func(t *testing.T) {
			nativeFirst := copyRestoreFixture(t, fresh)
			if _, err := nativeFirst.mergeBackup(ctx, laterDB, laterScratch, later, at.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := nativeFirst.mergeBackup(ctx, earlierDB, earlierScratch, earlier, at.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			history, err := nativeFirst.Messages(ctx, chat.ID, "", 10)
			if err != nil || len(history) != 2 || history[0].ID != answer.ID || history[1].ID != pending.ID ||
				history[1].OriginalText != firstBody || history[1].ProjectionSourceID == "" {
				t.Fatalf("native-first merge did not restore the verified correction: %+v, %v", history, err)
			}
			base, err := nativeFirst.MessageSource(ctx, pending.ID)
			if err != nil || base.RevisionID != terminal.RevisionID || base.OriginalText != composition.OriginalText {
				t.Fatalf("merge changed the native source: %+v, %v", base, err)
			}
			edit, err := nativeFirst.MessageSource(ctx, history[1].ProjectionSourceID)
			if err != nil || edit.RevisionID != history[1].RevisionID || edit.OriginalText != firstBody {
				t.Fatalf("merge changed the correction source: %+v, %v", edit, err)
			}
			if reply := history[0].Reply; reply == nil || !reply.Available || reply.MessageID != pending.ID {
				t.Fatalf("reply did not resolve to the native send: %+v", reply)
			}
			if edits, err := nativeFirst.ReadMessageEditHistory(ctx, pending.ID, false); err != nil || len(edits.Items) != 2 {
				t.Fatalf("edit history lost its original or verified correction: %+v, %v", edits, err)
			}
			_, before, err := nativeFirst.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := nativeFirst.mergeBackup(ctx, earlierDB, earlierScratch, earlier, at.Add(time.Hour)); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("repeating witness restore changed its correction: %+v, %v", result, err)
			}
			if _, after, err := nativeFirst.Snapshot(ctx); err != nil || after != before {
				t.Fatalf("reapplying the witness emitted events from %d to %d: %v", before, after, err)
			}
		})
	}
	if replay, err := destEngine.SendComposition(ctx, key, chat.ID, composition, nil); err != nil || !replay.Replayed || replay.Message.ID != pending.ID {
		t.Fatalf("original send receipt lost its identity: %+v %v", replay, err)
	}
	_, before, err := dest.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, backup := range []struct {
		manifest backupManifest
		database string
		scratch  string
	}{{later, laterDB, laterScratch}, {earlier, earlierDB, earlierScratch}} {
		if result, err := dest.mergeBackup(ctx, backup.database, backup.scratch, backup.manifest, at.Add(time.Hour)); err != nil || result != (RestoreSummary{}) {
			t.Fatalf("reapplying backup changed the merged send: %+v %v", result, err)
		}
	}
	if _, after, err := dest.Snapshot(ctx); err != nil || after != before {
		t.Fatalf("reapplying the backups emitted events from %d to %d: %v", before, after, err)
	}
	edit := correction
	edit.ID, edit.TargetID = "verified-edit", correction.ID
	const editedBody = "the new verified correction"
	if err := destEngine.Receive(ctx, application.Incoming{
		AccountID: chat.AccountID, Kind: kind, Address: chat.Address,
		ProviderID: "verified-edit", IsOwn: true, SenderAddress: "alice@localhost", Historical: true,
		CreatedAt: at.Add(2 * time.Second), OriginalText: editedBody, Correction: &edit,
	}); err != nil {
		t.Fatal(err)
	}
	assertHistory(dest, editedBody, witnessID)
	if edits, err := dest.ReadMessageEditHistory(ctx, pending.ID, false); err != nil || len(edits.Items) != correctionCount || edits.MessageID != pending.ID {
		t.Fatalf("correction history could not be read: %+v, %v", edits, err)
	}

	relayed, relayedDB, relayedScratch := backupRestoreFixture(t, dest, false)
	if _, err := fresh.mergeBackup(ctx, relayedDB, relayedScratch, relayed, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	assertHistory(fresh, editedBody, witnessID)
	if _, err := source.mergeBackup(ctx, relayedDB, relayedScratch, relayed, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	aliasID := witnessID
	if kind == "room" {
		aliasID = pending.ID
	}
	assertHistory(source, editedBody, aliasID)
}
