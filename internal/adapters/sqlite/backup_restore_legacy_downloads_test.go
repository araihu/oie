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

func TestBackupRestoreKeepsPostCleanupDownloadAfterReleasedProfileUpgrade(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	clearBackupDownloads(t, source, device, now)
	redownloadBackupImage(t, source, preview, device, now)

	upgraded := upgradeDownloadFixture(t, source, 39)
	var revision string
	if err := upgraded.readDB.QueryRowContext(ctx, `SELECT revision_id FROM download_cleanup_revisions WHERE device_id=?`, device).
		Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.ParseID(revision); err != nil {
		t.Fatalf("upgrade did not assign a valid cleanup revision: %q, %v", revision, err)
	}
	metadata, metadataDB, metadataScratch := backupRestoreFixture(t, upgraded, false)
	full, fullDB, fullScratch := backupRestoreFixture(t, upgraded, true)

	dest := openLegacyDownloadDestination(t)
	if _, err := dest.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 || policy.Generation != 1 {
		t.Fatalf("metadata-only restore did not transfer the cleanup generation: %+v, %v", policy, err)
	}
	if result, err := dest.mergeBackup(ctx, fullDB, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("full backup did not fill the upgraded download: %+v, %v", result, err)
	}
	if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 || policy.Generation != 1 {
		t.Fatalf("restore lost the upgraded profile's retained download: %+v, %v", policy, err)
	}
	if _, err := os.Stat(filepath.Join(dest.dir, "media", "images", preview.FileHash)); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoreKeepsOlderUnprovenDownloadPortableWithoutCertifyingIt(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	initial, initialDB, initialScratch := backupRestoreFixture(t, source, true)
	relay := openLegacyDownloadDestination(t)
	if _, err := relay.mergeBackup(ctx, initialDB, initialScratch, initial, now); err != nil {
		t.Fatal(err)
	}
	images := filepath.Join(source.dir, "media", "images")
	engine.SetPreviewServices(nil, mediafiles.New(images))
	if policy, err := engine.SetDownloadPolicy(ctx, device, 0, true); err != nil || policy.Count != 0 {
		t.Fatalf("settings did not clear the original: %+v, %v", policy, err)
	}
	clean, cleanDB, cleanScratch := backupRestoreFixture(t, source, false)
	if _, err := relay.mergeBackup(ctx, cleanDB, cleanScratch, clean, now); err != nil {
		t.Fatal(err)
	}
	if policy, err := relay.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 || policy.Generation != 1 {
		t.Fatalf("relay did not retain the older original under the imported cleanup: %+v, %v", policy, err)
	}

	migrateFixtureToVersion(t, relay, 43)
	older, olderDB, olderScratch := backupRestoreFixture(t, relay, true)
	legacyRestored := openLegacyDownloadDestination(t)
	if result, err := legacyRestored.mergeBackup(ctx, olderDB, olderScratch, older, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("a version 43 archive could not restore unproven media to a fresh profile: %+v, %v", result, err)
	}
	upgraded := upgradeDownloadFixture(t, relay, 0)

	for _, tc := range []struct {
		name  string
		store *Store
	}{
		{name: "migrated profile", store: upgraded},
		{name: "re-exported legacy archive", store: legacyRestored},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var proofCount int
			if err := tc.store.readDB.QueryRowContext(t.Context(), `SELECT count(*) FROM download_membership_cleanup_revisions WHERE device_id=?`, device).
				Scan(&proofCount); err != nil || proofCount != 0 {
				t.Fatalf("an inherited cleanup acquired a proof for an earlier membership: %d, %v", proofCount, err)
			}
			manifest, archive, scratch := backupRestoreFixture(t, tc.store, true)
			fresh := openLegacyDownloadDestination(t)
			if result, err := fresh.mergeBackup(t.Context(), archive, scratch, manifest, now); err != nil || result.MediaFiles != 1 {
				t.Fatalf("fresh restore could not recover unproven older media: %+v, %v", result, err)
			}
			if _, err := os.Stat(filepath.Join(fresh.dir, "media", "images", preview.FileHash)); err != nil {
				t.Fatal(err)
			}
			if result, err := source.mergeBackup(t.Context(), archive, scratch, manifest, now); err != nil || result.MediaFiles != 0 {
				t.Fatalf("legacy archive returned an original already cleared at the destination: %+v, %v", result, err)
			}
			if policy, err := source.DownloadPolicy(t.Context(), device); err != nil || policy.Count != 0 || policy.Generation != 1 {
				t.Fatalf("legacy restore changed cleared destination policy: %+v, %v", policy, err)
			}
			if _, err := os.Lstat(filepath.Join(images, preview.FileHash)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("legacy restore returned the deleted bytes: %v", err)
			}
		})
	}
}

func TestBackupRestoreCompletesPortableLegacyDownloadAfterMetadataOnlySnapshot(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	clearBackupDownloads(t, source, device, now)
	redownloadBackupImage(t, source, preview, device, now)
	upgraded := upgradeDownloadFixture(t, source, 43)
	metadata, metadataArchive, metadataScratch := backupRestoreFixture(t, upgraded, false)
	full, fullArchive, fullScratch := backupRestoreFixture(t, upgraded, true)

	for _, tc := range []struct {
		name           string
		clearLocally   bool
		wantFiles      int
		wantGeneration int64
	}{
		{name: "complete the earlier restore", wantFiles: 1, wantGeneration: 1},
		{name: "clear before restoring bytes", clearLocally: true, wantGeneration: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest := openLegacyDownloadDestination(t)
			if _, err := dest.mergeBackup(ctx, metadataArchive, metadataScratch, metadata, now); err != nil {
				t.Fatal(err)
			}
			if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 || policy.Generation != 1 {
				t.Fatalf("metadata-only restore did not install the cleanup without media: %+v, %v", policy, err)
			}
			dest = upgradeDownloadFixture(t, dest, 0)
			if tc.clearLocally {
				clearBackupDownloads(t, dest, device, now.Add(time.Minute))
			}
			if result, err := dest.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != tc.wantFiles {
				t.Fatalf("full legacy restore after metadata: %+v, %v", result, err)
			}
			if result, err := dest.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 0 {
				t.Fatalf("legacy restore changed on repeat: %+v, %v", result, err)
			}
			if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != tc.wantFiles || policy.Generation != tc.wantGeneration {
				t.Fatalf("partial legacy restore invalidated the device policy: %+v, %v", policy, err)
			}
			var proofs int
			if err := dest.readDB.QueryRowContext(ctx, "SELECT count(*) FROM download_membership_cleanup_revisions WHERE device_id=?", device).
				Scan(&proofs); err != nil || proofs != 0 {
				t.Fatalf("receiving legacy bytes manufactured a cleanup proof: %d, %v", proofs, err)
			}
		})
	}
}

func upgradeDownloadFixture(t *testing.T, store *Store, oldVersion uint) *Store {
	t.Helper()
	if oldVersion != 0 {
		migrateFixtureToVersion(t, store, oldVersion)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), store.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	return upgraded
}

func openLegacyDownloadDestination(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.Context(), t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
