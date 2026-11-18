package sqlite

import (
	"slices"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreTerminalEditWithPartialWitnessAncestry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                              string
		observeRoot, noNative, lateFailed bool
	}{
		{name: "only correction has been observed"},
		{name: "original and correction have been observed under different IDs", observeRoot: true, noNative: true},
		{name: "witnessed original correction later rejected", observeRoot: true, lateFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, editor, chat := outboxFixture(t)
			editor.SetProtocols(&editProtocol{})
			ctx := t.Context()
			var destination *Store
			if tc.noNative {
				destination = copyRestoreFixture(t, source)
			}
			original := ownSubmittedEdit(t, source, editor, chat, "retained original native body")
			if destination == nil {
				destination = copyRestoreFixture(t, source)
			}
			first := queuedEdit(t, editor, original.ID, "first submitted correction seen in MAM")
			finishEdit(t, source, first.Command.ID, "submitted")
			latest := queuedEdit(t, editor, original.ID, "later correction based on first acceptance")
			last := finishEdit(t, source, latest.Command.ID, "submitted")
			if tc.lateFailed {
				if err := editor.Reject(ctx, application.Rejected{
					AccountID: chat.AccountID, MessageID: string(first.Command.ID), Address: chat.Address,
				}); err != nil {
					t.Fatal(err)
				}
			}
			terminal, err := editor.MessageEdit(ctx, first.Command.ID)
			wantState := "submitted"
			if tc.lateFailed {
				wantState = "failed"
			}
			if err != nil || terminal.State != wantState {
				t.Fatalf("fixture did not retain the requested terminal outcome: %+v, %v", terminal, err)
			}

			at := time.Now().UTC()
			const scope, author = "xmpp:correct:sender:alice@localhost", "alice@localhost"
			receiver := testRestoreEngine(t, destination)
			receiver.SetProtocols(&outboundProtocol{})
			receive := func(id, target domain.ID, body, archiveID string) {
				t.Helper()
				if err := receiver.Receive(ctx, application.Incoming{
					AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", Historical: true, IsOwn: true,
					SenderAddress: author, ProviderID: string(id), OriginalText: body, CreatedAt: at,
					ArchiveScope: author, ArchiveID: archiveID,
					Correction: &domain.MessageCorrection{Scope: scope, Author: author, ID: string(id), TargetID: string(target)},
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.observeRoot {
				receive(original.ID, "", original.OriginalText, "observed-original")
			}
			receive(first.Command.ID, original.ID, first.Command.OriginalText, "observed-edit")
			alias := dbgen.GetArchiveAliasParams{
				AccountID: string(chat.AccountID), Scope: author, ArchiveID: "observed-edit",
			}
			observed, err := destination.queries.GetArchiveAlias(ctx, alias)
			if err != nil || observed == string(first.Command.ID) {
				t.Fatalf("fixture did not retain a separate physical witness: %q, %v", observed, err)
			}
			witness, err := destination.MessageSource(ctx, domain.ID(observed))
			if err != nil {
				t.Fatal(err)
			}

			check := func(store *Store) {
				t.Helper()
				engine := testRestoreEngine(t, store)
				for _, edit := range []domain.MessageEditCommand{terminal, last} {
					got, err := engine.EditMessage(ctx, edit.ID, original.ID, edit.ExpectedRevisionID, edit.OriginalText)
					if err != nil || !got.Replayed || got.Command.State != edit.State || got.Command.RevisionID != edit.RevisionID {
						t.Fatalf("restore changed the accepted terminal command: %+v, %v", got, err)
					}
				}
				if stored, err := store.MessageSource(ctx, witness.ID); err != nil || stored.RevisionID != witness.RevisionID || stored.ProviderID != witness.ProviderID {
					t.Fatalf("restore changed the earlier physical witness: %+v, %v", stored, err)
				}
				if owner, err := store.queries.GetArchiveAlias(ctx, alias); err != nil || owner != observed {
					t.Fatalf("restore moved the witnessed archive key: %q, %v", owner, err)
				}
				timeline, err := store.Messages(ctx, chat.ID, "", 10)
				if err != nil || len(timeline) != 1 || timeline[0].ID != original.ID || timeline[0].OriginalText != last.OriginalText {
					t.Fatalf("restore changed the logical timeline: %+v, %v", timeline, err)
				}
				history := editHistory(t, engine, original.ID, "", 10)
				witnessed := slices.ContainsFunc(history.Items, func(item domain.MessageEditHistoryItem) bool {
					return item.SourceMessageID == witness.ID && item.RevisionID == witness.RevisionID
				})
				active := slices.ContainsFunc(history.Items, func(item domain.MessageEditHistoryItem) bool {
					return item.IsCurrent && item.SourceMessageID == last.ID &&
						len(item.ParentRevisionIDs) == 1 && item.ParentRevisionIDs[0] == witness.RevisionID
				})
				if len(history.Items) != 3 || !witnessed || !active {
					t.Fatalf("accepted descendant lost its witnessed parent: %+v", history)
				}
			}
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, at)
			check(destination)
			relayedManifest, relayed, relayedScratch := backupRestoreFixture(t, destination, false)
			relay := openLegacyDownloadDestination(t)
			assertLocalEditRestoreRepeats(t, relay, relayedManifest, relayed, relayedScratch, at)
			check(relay)
			assertLocalEditRestoreRepeats(t, relay, manifest, archive, scratch, at)
			check(relay)
		})
	}
}
