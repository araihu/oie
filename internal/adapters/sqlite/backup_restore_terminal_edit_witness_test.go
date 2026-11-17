package sqlite

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsDirectWitnessWhenImportingTerminalEdit(t *testing.T) {
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	ctx := t.Context()
	original := ownSubmittedEdit(t, source, editor, chat, "the original native body")
	destination, conflicting := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	missingProof := copyRestoreFixture(t, source)
	first := queuedEdit(t, editor, original.ID, "the submitted edit seen in MAM")
	finishEdit(t, source, first.Command.ID, "submitted")
	latest := queuedEdit(t, editor, original.ID, "the later edit accepted on the native revision")
	finishEdit(t, source, latest.Command.ID, "submitted")
	at := time.Now().UTC()
	const scope, author, archiveID = "xmpp:correct:sender:alice@localhost", "alice@localhost", "verified-edit-witness"
	receive := func(store *Store, editBody string) {
		t.Helper()
		receiver := testRestoreEngine(t, store)
		receiver.SetProtocols(&outboundProtocol{})
		for _, observed := range []struct {
			id, target domain.ID
			body       string
			archive    string
		}{
			{original.ID, "", original.OriginalText, "verified-original"},
			{first.Command.ID, original.ID, editBody, archiveID},
		} {
			if err := receiver.Receive(ctx, application.Incoming{
				AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", Historical: true, IsOwn: true,
				SenderAddress: author, ProviderID: string(observed.id), OriginalText: observed.body, CreatedAt: at,
				ArchiveScope: author, ArchiveID: observed.archive,
				Correction: &domain.MessageCorrection{Scope: scope, Author: author, ID: string(observed.id), TargetID: string(observed.target)},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	receive(destination, first.Command.OriginalText)
	receive(conflicting, "a conflicting body using the same edit ID")
	plainObserver := testRestoreEngine(t, missingProof)
	plainObserver.SetProtocols(&outboundProtocol{})
	if err := plainObserver.Receive(ctx, application.Incoming{
		AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", Historical: true, IsOwn: true,
		SenderAddress: author, ProviderID: string(first.Command.ID), OriginalText: first.Command.OriginalText, CreatedAt: at,
		ArchiveScope: author, ArchiveID: archiveID,
	}); err != nil {
		t.Fatal(err)
	}
	aliasKey := dbgen.GetArchiveAliasParams{AccountID: string(chat.AccountID), Scope: author, ArchiveID: archiveID}
	witnessID, err := destination.queries.GetArchiveAlias(ctx, aliasKey)
	if err != nil || witnessID == string(first.Command.ID) {
		t.Fatalf("fixture lacks an independently stored edit witness: %s, %v", witnessID, err)
	}
	witness, err := destination.MessageSource(ctx, domain.ID(witnessID))
	if err != nil {
		t.Fatal(err)
	}
	if m, err := destination.Message(ctx, original.ID); err != nil || !m.Edited || m.ProjectionSourceID != witness.ID {
		t.Fatalf("fixture did not use the independent witness: %+v, %v", m, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := conflicting.mergeBackup(ctx, archive, scratch, manifest, at); err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
		t.Fatalf("the different correction body did not retain its collision: %v", err)
	}
	assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, at)
	check := func(store *Store) {
		t.Helper()
		host := testRestoreEngine(t, store)
		for _, command := range []domain.MessageEditCommand{first.Command, latest.Command} {
			if got, err := host.EditMessage(ctx, command.ID, original.ID, command.ExpectedRevisionID, command.OriginalText); err != nil || !got.Replayed || got.Command.State != "submitted" {
				t.Fatalf("restore lost the native terminal acceptance: %+v, %v", got, err)
			}
		}
		if retained, err := store.MessageSource(ctx, witness.ID); err != nil || retained.RevisionID != witness.RevisionID || retained.ProviderID != witness.ProviderID {
			t.Fatalf("restore changed the independent archived revision: %+v, %v", retained, err)
		}
		if owner, err := store.queries.GetArchiveAlias(ctx, aliasKey); err != nil || owner != witnessID {
			t.Fatalf("restore moved the witnessed archive key: %s, %v", owner, err)
		}
		timeline, err := store.Messages(ctx, chat.ID, "", 10)
		if err != nil || len(timeline) != 1 || timeline[0].ID != original.ID || timeline[0].OriginalText != latest.Command.OriginalText {
			t.Fatalf("the restored edit chain left duplicate or unrelated history: %+v, %v", timeline, err)
		}
		history := editHistory(t, host, original.ID, "", 10)
		witnessed := slices.ContainsFunc(history.Items, func(item domain.MessageEditHistoryItem) bool {
			return item.SourceMessageID == witness.ID && item.RevisionID == witness.RevisionID
		})
		parentPreserved := slices.ContainsFunc(history.Items, func(item domain.MessageEditHistoryItem) bool {
			return item.IsCurrent && item.SourceMessageID == latest.Command.ID && len(item.ParentRevisionIDs) == 1 && item.ParentRevisionIDs[0] == witness.RevisionID
		})
		if len(history.Items) != 3 || !witnessed || !parentPreserved {
			t.Fatalf("later native acceptance did not resolve to the exact witnessed parent: %+v", history)
		}
	}
	check(destination)
	forwarded, forwardedDB, forwardedScratch := backupRestoreFixture(t, destination, false)
	relay := openLegacyDownloadDestination(t)
	assertLocalEditRestoreRepeats(t, relay, forwarded, forwardedDB, forwardedScratch, at)
	check(relay)
	t.Run("native before matching witness", func(t *testing.T) {
		native := openLegacyDownloadDestination(t)
		assertLocalEditRestoreRepeats(t, native, manifest, archive, scratch, at)
		assertLocalEditRestoreRepeats(t, native, forwarded, forwardedDB, forwardedScratch, at)
		if owner, err := native.queries.GetArchiveAlias(ctx, aliasKey); err != nil || owner != string(first.Command.ID) {
			t.Fatalf("restoring the matching witness lost its archive key: %q, %v", owner, err)
		}
	})
	t.Run("native before conflicting witness", func(t *testing.T) {
		native := openLegacyDownloadDestination(t)
		assertLocalEditRestoreRepeats(t, native, manifest, archive, scratch, at)
		other, otherDB, otherScratch := backupRestoreFixture(t, conflicting, false)
		if _, err := native.mergeBackup(ctx, otherDB, otherScratch, other, at); err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
			t.Fatalf("a distinct correction body did not keep the provider identity conflict: %v", err)
		}
	})
	t.Run("native before witness without correction proof", func(t *testing.T) {
		native := openLegacyDownloadDestination(t)
		assertLocalEditRestoreRepeats(t, native, manifest, archive, scratch, at)
		other, otherDB, otherScratch := backupRestoreFixture(t, missingProof, false)
		if _, err := native.mergeBackup(ctx, otherDB, otherScratch, other, at); err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
			t.Fatalf("text alone was accepted as the same correction: %v", err)
		}
	})
}
