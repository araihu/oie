package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsPriorGlobalAdmissionWithNamedMedia(t *testing.T) {
	legacy, named, old, _, device, now := independentlyDownloadedLegacyImage(t)
	ctx := t.Context()
	first, firstDB, firstScratch := backupRestoreFixture(t, legacy, false)
	chat, err := legacy.Conversation(ctx, old.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	// Both forks also know a separate message using the same URL and bytes.
	// The admission recorded above belongs only to the first message.
	separate := replySeed(t, legacy, chat, old.URL, now, nil, nil)
	if err := named.ReceiveMessage(ctx, chat, separate, migrationID()); err != nil {
		t.Fatal(err)
	}
	separatePreview, mintedDevice := seedBackupDownloadedImageForMessage(t, legacy, separate, old.URL, now)
	if _, err := legacy.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", mintedDevice); err != nil {
		t.Fatal(err)
	}
	independent, _ := seedBackupDownloadedImageForMessage(t, named, separate, old.URL, now)
	if separatePreview.FileHash != old.FileHash || independent.FileHash != old.FileHash || separatePreview.ID == independent.ID {
		t.Fatal("expected shared bytes with distinct messages and independently assigned preview IDs")
	}
	global, globalDB, globalScratch := backupRestoreFixture(t, legacy, true)
	active, activeDB, activeScratch := backupRestoreFixture(t, named, false)
	activeFull, activeFullDB, activeFullScratch := backupRestoreFixture(t, named, true)
	migrateFixtureToVersion(t, named, 39)
	released, releasedDB, releasedScratch := backupRestoreFixture(t, named, false)
	upgraded := upgradeDownloadFixture(t, named, 0)
	var inferred, hardCount int
	if err := upgraded.readDB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM active_legacy_image_claims),
                                                         (SELECT count(*) FROM legacy_image_claims)`).Scan(&inferred, &hardCount); err != nil || inferred != 2 || hardCount != 0 {
		t.Fatalf("released upgrade should retain both active copies as inferred evidence: %d inferred, %d hard, error %v", inferred, hardCount, err)
	}
	afterUpgrade, upgradeDB, upgradeScratch := backupRestoreFixture(t, upgraded, false)

	claimed := openLegacyDownloadDestination(t)
	if _, err := claimed.mergeBackup(ctx, globalDB, globalScratch, global, now); err != nil {
		t.Fatal(err)
	}
	if policy, err := testRestoreEngine(t, claimed).SetDownloadPolicy(ctx, device, 0, false); err != nil || policy.Count != 2 {
		t.Fatalf("Settings did not actually claim both global copies: %+v, %v", policy, err)
	}
	hard, hardDB, hardScratch := backupRestoreFixture(t, claimed, false)

	for _, tc := range []struct {
		name                        string
		namedFirst, withNamedBytes  bool
		relayMetadata, clear, claim bool
		olderNamedArchive           bool
		upgradedNamedArchive        bool
		want                        int
	}{
		{name: "earlier exact admission", want: 1},
		{name: "earlier exact admission with active named bytes", withNamedBytes: true, want: 1},
		{name: "earlier admission after metadata re-export", relayMetadata: true, want: 1},
		{name: "earlier admission with released named archive", olderNamedArchive: true, want: 1},
		{name: "earlier admission with freshly upgraded named archive", upgradedNamedArchive: true, want: 1},
		{name: "named evidence before admission", namedFirst: true, relayMetadata: true},
		{name: "explicit local clear", clear: true},
		{name: "explicit claim from companion profile", claim: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			destination := openLegacyDownloadDestination(t)
			merge := func(m backupManifest, db, scratch string) {
				t.Helper()
				if _, err := destination.mergeBackup(ctx, db, scratch, m, now); err != nil {
					t.Fatal(err)
				}
			}
			if tc.namedFirst {
				merge(active, activeDB, activeScratch)
				merge(first, firstDB, firstScratch)
			} else {
				merge(first, firstDB, firstScratch)
				var admitted int
				if err := destination.readDB.QueryRowContext(ctx, "SELECT count(*) FROM unclaimed_legacy_download_restore_admissions").Scan(&admitted); err != nil || admitted != 1 {
					t.Fatalf("first snapshot did not admit exactly one companion: %d, %v", admitted, err)
				}
				switch {
				case tc.withNamedBytes:
					merge(activeFull, activeFullDB, activeFullScratch)
				case tc.olderNamedArchive:
					merge(released, releasedDB, releasedScratch)
				case tc.upgradedNamedArchive:
					merge(afterUpgrade, upgradeDB, upgradeScratch)
				default:
					merge(active, activeDB, activeScratch)
				}
			}
			if tc.relayMetadata {
				manifest, db, scratch := backupRestoreFixture(t, destination, false)
				destination = openLegacyDownloadDestination(t)
				merge(manifest, db, scratch)
			}
			if tc.clear {
				if policy, err := testRestoreEngine(t, destination).SetDownloadPolicy(ctx, device, 0, true); err != nil || policy.Generation != 1 {
					t.Fatalf("Settings did not record an actual clear: %+v, %v", policy, err)
				}
			}
			if tc.claim {
				merge(hard, hardDB, hardScratch)
			}
			merge(global, globalDB, globalScratch)
			check := func() {
				t.Helper()
				if policy, err := destination.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != tc.want {
					t.Fatalf("restored wrong number of global memberships: %+v, %v", policy, err)
				}
				assertLegacyImageMembership(t, destination, old.MessageID, tc.want)
				assertLegacyImageMembership(t, destination, separate.ID, 0)
			}
			check()
			if result, err := destination.mergeBackup(ctx, globalDB, globalScratch, global, now.Add(time.Minute)); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("restoring the same companion again: %+v, %v", result, err)
			}
			check()
			if tc.withNamedBytes {
				manifest, db, scratch := backupRestoreFixture(t, destination, true)
				destination = openLegacyDownloadDestination(t)
				merge(manifest, db, scratch)
				check()
			}
		})
	}
}

func assertLegacyImageMembership(t *testing.T, store *Store, id domain.ID, want int) {
	t.Helper()
	var count int
	if err := store.readDB.QueryRowContext(t.Context(), `
SELECT count(*) FROM device_downloads d JOIN message_previews p ON p.id=d.source_id
WHERE d.device_id='legacy' AND d.kind='image' AND p.message_id=?`, id).Scan(&count); err != nil || count != want {
		t.Fatalf("message %s has %d global memberships, want %d, error %v", id, count, want, err)
	}
}
