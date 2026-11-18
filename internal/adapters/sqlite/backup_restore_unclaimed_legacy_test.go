package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreCompletesUnclaimedLegacyMediaWithoutUndoingClear(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", device); err != nil {
		t.Fatal(err)
	}
	initial, initialDB, initialScratch := backupRestoreFixture(t, source, true)
	relay := openLegacyDownloadDestination(t)
	if result, err := relay.mergeBackup(ctx, initialDB, initialScratch, initial, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("initial archive did not restore the unclaimed original: %+v, %v", result, err)
	}

	engine.SetPreviewServices(nil, mediafiles.New(filepath.Join(source.dir, "media", "images")))
	if policy, err := engine.SetDownloadPolicy(ctx, device, 0, true); err != nil || policy.Count != 0 || policy.Generation != 1 {
		t.Fatalf("source Settings did not clear the original: %+v, %v", policy, err)
	}
	cleared, clearedDB, clearedScratch := backupRestoreFixture(t, source, false)
	if _, err := relay.mergeBackup(ctx, clearedDB, clearedScratch, cleared, now); err != nil {
		t.Fatal(err)
	}
	if legacy, err := relay.DownloadPolicy(ctx, "legacy"); err != nil || legacy.Count != 1 {
		t.Fatalf("cleanup import removed the relay's retained original: %+v, %v", legacy, err)
	}
	metadata, metadataDB, metadataScratch := backupRestoreFixture(t, relay, false)
	full, fullDB, fullScratch := backupRestoreFixture(t, relay, true)
	partial := openLegacyDownloadDestination(t)
	if _, err := partial.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	relayed, relayedDB, relayedScratch := backupRestoreFixture(t, partial, false)

	for _, tc := range []struct {
		name       string
		relayAgain bool
		clear      bool
	}{
		{name: "settings save"},
		{name: "relayed admission", relayAgain: true},
		{name: "local clear", clear: true},
		{name: "local clear after relay", relayAgain: true, clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			dest := openLegacyDownloadDestination(t)
			first, db, scratch := metadata, metadataDB, metadataScratch
			if tc.relayAgain {
				first, db, scratch = relayed, relayedDB, relayedScratch
			}
			if _, err := dest.mergeBackup(ctx, db, scratch, first, now); err != nil {
				t.Fatal(err)
			}
			host := testRestoreEngine(t, dest)
			if policy, err := host.SetDownloadPolicy(ctx, device, 0, tc.clear); err != nil || policy.Count != 0 {
				t.Fatalf("unexpected device state before media arrives: %+v, %v", policy, err)
			}
			want := 1
			if tc.clear {
				want = 0
				if _, err := dest.mergeBackup(ctx, db, scratch, first, now); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := dest.mergeBackup(ctx, fullDB, fullScratch, full, now); err != nil || result.MediaFiles != want {
				t.Fatalf("full archive restored the wrong media: %+v, %v", result, err)
			}
			filename := filepath.Join(dest.dir, "media", "images", preview.FileHash)
			if _, err := os.Stat(filename); (tc.clear && !errors.Is(err, os.ErrNotExist)) || (!tc.clear && err != nil) {
				t.Fatalf("unexpected state of the original bytes: %v", err)
			}
			if policy, err := host.SetDownloadPolicy(ctx, device, 0, false); err != nil || policy.Count != want {
				t.Fatalf("Settings did not claim exactly the restored originals: %+v, %v", policy, err)
			}
			if result, err := dest.mergeBackup(ctx, fullDB, fullScratch, full, now); err != nil || result.MediaFiles != 0 {
				t.Fatalf("replaying the companion archive changed restored files: %+v, %v", result, err)
			}
			if legacy, err := dest.DownloadPolicy(ctx, "legacy"); err != nil || legacy.Count != 0 {
				t.Fatalf("replaying the archive re-created the unclaimed membership: %+v, %v", legacy, err)
			}
		})
	}
}

func TestBackupRestoreKeepsClaimedLegacyImageOnNamedDevice(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	first, device := seedBackupDownloadedImage(t, source, chat, now)
	if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", device); err != nil {
		t.Fatal(err)
	}
	migrateFixtureToVersion(t, source, 47)
	metadata, metadataArchive, metadataScratch := backupRestoreFixture(t, source, false)
	full, fullArchive, fullScratch := backupRestoreFixture(t, source, true)
	destination := openLegacyDownloadDestination(t)
	if _, err := destination.mergeBackup(ctx, metadataArchive, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	if result, err := destination.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("companion archive did not restore the unclaimed original: %+v, %v", result, err)
	}
	host := testRestoreEngine(t, destination)
	if policy, err := host.SetDownloadPolicy(ctx, device, 0, false); err != nil || policy.Count != 1 || policy.Generation != 0 {
		t.Fatalf("Settings did not claim the original: %+v, %v", policy, err)
	}
	if result, err := destination.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("repeating the archive changed the retained file: %+v, %v", result, err)
	}
	if policy, err := destination.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 0 {
		t.Fatalf("replay recreated a global membership for the claimed original: %+v, %v", policy, err)
	}

	second, secondDevice := seedBackupDownloadedImage(t, source, chat, now)
	if second.FileHash != first.FileHash || second.MessageID == first.MessageID {
		t.Fatal("fixture must contain the same bytes in a separate message")
	}
	if _, err := source.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", secondDevice); err != nil {
		t.Fatal(err)
	}
	expanded, expandedArchive, expandedScratch := backupRestoreFixture(t, source, true)
	if _, err := destination.mergeBackup(ctx, expandedArchive, expandedScratch, expanded, now); err != nil {
		t.Fatal(err)
	}
	if policy, err := destination.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 1 {
		t.Fatalf("replay did not restore only the separate legacy original: %+v, %v", policy, err)
	}
	if preview, err := destination.Preview(ctx, second.ID); err != nil || preview.FileHash != second.FileHash {
		t.Fatalf("separate message lost its image sharing the same bytes: %+v, %v", preview, err)
	}
}

func TestBackupRestoreKeepsExpiredLegacyImageClaimAfterReexport(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now.AddDate(0, 0, -2))
	if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", device); err != nil {
		t.Fatal(err)
	}
	metadata, metadataArchive, metadataScratch := backupRestoreFixture(t, source, false)
	full, fullArchive, fullScratch := backupRestoreFixture(t, source, true)
	destination := openLegacyDownloadDestination(t)
	if _, err := destination.mergeBackup(ctx, metadataArchive, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	host := testRestoreEngine(t, destination)
	local, err := host.ConversationPreviews(ctx, chat.ID, []domain.ID{preview.MessageID})
	if err != nil || len(local) != 1 || local[0].ID == preview.ID {
		t.Fatalf("fixture did not independently establish a local preview identity: %+v, %v", local, err)
	}
	if result, err := destination.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("initial archive did not restore the original: %+v, %v", result, err)
	}
	images := filepath.Join(destination.dir, "media", "images")
	host.SetPreviewServices(nil, mediafiles.New(images))
	if policy, err := host.SetDownloadPolicy(ctx, device, 1, false); err != nil || policy.Count != 0 || policy.Generation != 0 {
		t.Fatalf("Settings did not claim and expire the older global image: %+v, %v", policy, err)
	}
	if _, err := os.Stat(filepath.Join(images, preview.FileHash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Settings did not remove the expired original: %v", err)
	}

	relayed, relayedArchive, relayedScratch := backupRestoreFixture(t, destination, false)
	restored := openLegacyDownloadDestination(t)
	if _, err := restored.mergeBackup(ctx, relayedArchive, relayedScratch, relayed, now); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{destination, restored} {
		if result, err := store.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 0 {
			t.Fatalf("the stale archive revived the expired global original: %+v, %v", result, err)
		}
		if policy, err := store.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 0 {
			t.Fatalf("replaying the archive recreated the expired global membership: %+v, %v", policy, err)
		}
		if _, err := os.Stat(filepath.Join(store.dir, "media", "images", preview.FileHash)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the expired global original returned to disk: %v", err)
		}
	}
}
