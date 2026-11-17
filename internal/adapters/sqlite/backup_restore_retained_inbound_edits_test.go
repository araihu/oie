package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsVerifiedEditsOfRetainedIncomingOriginal(t *testing.T) {
	for _, kind := range []string{"room", "direct"} {
		t.Run(kind, func(t *testing.T) {
			source, sourceEngine, chat := outboxFixture(t)
			scope, author := "xmpp:correct:sender:bob@localhost", "bob@localhost"
			if kind == "room" {
				sourceEngine, chat = promoteBackupRoomFixture(t, source, chat)
				scope, author = "xmpp:correct:room:"+chat.Address+":verified-occupant", chat.Address+"/Bob"
			}
			sourceEngine.SetProtocols(&outboundProtocol{})
			ctx := t.Context()
			now := time.Now().UTC()
			oldAt, recent := now.Add(-48*time.Hour), now.Add(-time.Hour)
			fresh := copyRestoreFixture(t, source)
			receiveCorrection(t, sourceEngine, chat, "older-original", "the old incoming body", "", scope, author, oldAt)
			original := correctionMessages(t, sourceEngine, chat)[0]
			destination := copyRestoreFixture(t, source)
			destEngine := testRestoreEngine(t, destination)
			if kind == "direct" {
				if _, err := destEngine.SetMessageStar(ctx, migrationID(), original.ID, "", true); err != nil {
					t.Fatal(err)
				}
			}
			days := 1
			for _, host := range []*application.Engine{destEngine, testRestoreEngine(t, fresh)} {
				if _, err := host.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := destination.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || n != 0 {
				t.Fatalf("destination did not retain the original: %d, %v", n, err)
			}
			relayDestination := copyRestoreFixture(t, destination)
			unrelated := replySeed(t, source, chat, "an unrelated old message", oldAt, nil, nil)
			const corrected = "the verified recent correction"
			receiveCorrection(t, sourceEngine, chat, "recent-edit", corrected, "older-original", scope, author, recent)
			witnessed, err := source.Message(ctx, original.ID)
			if err != nil || !witnessed.Edited || witnessed.OriginalText != corrected || witnessed.ProjectionSourceID == "" {
				t.Fatalf("source did not accept its verified correction: %+v, %v", witnessed, err)
			}
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if result, err := fresh.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("restore imported an original outside retention into a fresh destination: %+v, %v", result, err)
			}
			assertCorrection := func(store *Store) {
				t.Helper()
				got, err := store.Message(ctx, original.ID)
				if err != nil || !got.Edited || got.OriginalText != corrected || got.ProjectionSourceID != witnessed.ProjectionSourceID {
					t.Fatalf("restore did not project the verified correction: %+v, %v", got, err)
				}
				raw, err := store.MessageSource(ctx, original.ID)
				if err != nil || raw.RevisionID != original.RevisionID || raw.OriginalText != original.OriginalText {
					t.Fatalf("restore changed the original source revision: %+v, %v", raw, err)
				}
				if _, err := store.Message(ctx, unrelated.ID); !errors.Is(err, application.ErrNotFound) {
					t.Fatalf("unrelated history escaped the cutoff: %v", err)
				}
			}
			if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("restore added visible messages alongside the corrected root: %+v, %v", result, err)
			}
			assertCorrection(destination)
			_, before, err := destination.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("reapplying the correction changed the restore: %+v, %v", result, err)
			}
			if _, after, err := destination.Snapshot(ctx); err != nil || before != after {
				t.Fatalf("reapplying the correction emitted events from %d to %d: %v", before, after, err)
			}
			relay, relayArchive, relayScratch := backupRestoreFixture(t, destination, false)
			if result, err := relayDestination.mergeBackup(ctx, relayArchive, relayScratch, relay, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("re-export added visible messages alongside the corrected root: %+v, %v", result, err)
			}
			assertCorrection(relayDestination)
		})
	}
}
