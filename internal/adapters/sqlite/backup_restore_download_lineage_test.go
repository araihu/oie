package sqlite

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreFillsPostCleanupDownloadAfterMetadataOnlyRestore(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	old, oldArchive, oldScratch := backupRestoreFixture(t, source, true)
	clearBackupDownloads(t, source, device, now)
	redownloadBackupImage(t, source, preview, device, now)
	metadata, metadataArchive, metadataScratch := backupRestoreFixture(t, source, false)
	full, fullArchive, fullScratch := backupRestoreFixture(t, source, true)

	dir := t.TempDir()
	dest, err := Open(ctx, dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(ctx, metadataArchive, metadataScratch, metadata, now); err != nil {
		_ = dest.Close()
		t.Fatal(err)
	}
	if err := dest.Close(); err != nil {
		t.Fatal(err)
	}
	dest, err = Open(ctx, dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dest.Close() })
	if result, err := dest.mergeBackup(ctx, oldArchive, oldScratch, old, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("older archive revived a pre-cleanup download: %+v, %v", result, err)
	}
	if result, err := dest.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("same post-cleanup download could not be restored: %+v, %v", result, err)
	}
	if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 || policy.Generation != 1 {
		t.Fatalf("download membership or cleanup generation was lost: %+v, %v", policy, err)
	}
	if result, err := dest.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("importing the same archive again changed the download: %+v, %v", result, err)
	}
}

func TestBackupRestorePreservesIndependentLocalDownloadCleanup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for _, clears := range []int{1, 2} {
		name := "equal source generation"
		if clears > 1 {
			name = "higher source generation"
		}
		t.Run(name, func(t *testing.T) {
			source, _, chat := outboxFixture(t)
			now := time.Now().UTC()
			preview, device := seedBackupDownloadedImage(t, source, chat, now)
			dest := copyRestoreFixture(t, source)
			clearBackupDownloads(t, dest, device, now)
			for i := 0; i < clears; i++ {
				clearBackupDownloads(t, source, device, now)
			}
			redownloadBackupImage(t, source, preview, device, now)
			foreign, foreignArchive, foreignScratch := backupRestoreFixture(t, source, true)

			for i := 0; i < 2; i++ {
				if result, err := dest.mergeBackup(ctx, foreignArchive, foreignScratch, foreign, now); err != nil || result.MediaFiles != 0 {
					t.Fatalf("independent cleanup did not protect the destination: %+v, %v", result, err)
				}
			}
			if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 || policy.Generation != 1 {
				t.Fatalf("foreign cleanup invalidated the destination generation: %+v, %v", policy, err)
			}

			// A later backup of the destination's own post-cleanup downloads
			// must still carry the same recognized cleanup revision.
			data, err := os.ReadFile(filepath.Join(source.dir, "media", "images", preview.FileHash))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mediafiles.New(filepath.Join(dest.dir, "media", "images")).Put(ctx, data, true); err != nil {
				t.Fatal(err)
			}
			redownloadBackupImage(t, dest, preview, device, now)
			metadata, metadataArchive, metadataScratch := backupRestoreFixture(t, dest, false)
			full, fullArchive, fullScratch := backupRestoreFixture(t, dest, true)
			restored, err := Open(ctx, t.TempDir(), migrationID())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restored.Close() })
			if _, err := restored.mergeBackup(ctx, metadataArchive, metadataScratch, metadata, now); err != nil {
				t.Fatal(err)
			}
			if result, err := restored.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
				t.Fatalf("foreign cleanup invalidated later backups of the destination: %+v, %v", result, err)
			}
		})
	}
}

func TestBackupRestoreOpensOlderDownloadArchiveWithoutCleanupRevision(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	dest := copyRestoreFixture(t, source)
	clearBackupDownloads(t, dest, device, now)
	clearBackupDownloads(t, source, device, now)
	redownloadBackupImage(t, source, preview, device, now)
	for _, tc := range []struct {
		name    string
		version uint
	}{
		{name: "v43 without membership proof", version: 43},
		{name: "v42 without cleanup revision", version: 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			migrateFixtureToVersion(t, source, tc.version)
			older, archive, scratch := backupRestoreFixture(t, source, true)
			fresh, err := Open(ctx, t.TempDir(), migrationID())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fresh.Close() })
			if result, err := fresh.mergeBackup(ctx, archive, scratch, older, now); err != nil || result.MediaFiles != 1 {
				t.Fatalf("an older archive could not restore media to a fresh destination: %+v, %v", result, err)
			}
			if result, err := dest.mergeBackup(ctx, archive, scratch, older, now); err != nil || result.MediaFiles != 0 {
				t.Fatalf("an older archive bypassed the local cleanup: %+v, %v", result, err)
			}
			if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 || policy.Generation != 1 {
				t.Fatalf("older archive invalidated the destination cleanup: %+v, %v", policy, err)
			}
		})
	}
}

func clearBackupDownloads(t *testing.T, store *Store, device domain.ID, now time.Time) {
	t.Helper()
	if err := store.PurgeDownloads(t.Context(), device, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil {
		t.Fatal(err)
	}
}

func redownloadBackupImage(t *testing.T, store *Store, preview domain.MessagePreview, device domain.ID, now time.Time) {
	t.Helper()
	ctx := t.Context()
	prior, err := store.Preview(ctx, preview.ID)
	if err != nil || prior.DownloadState != "not_downloaded" {
		t.Fatalf("download cleanup did not detach the original: %+v, %v", prior, err)
	}
	preview.RevisionID, preview.UpdatedAt = migrationID(), now
	if err := store.SavePreview(ctx, preview, prior.RevisionID); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: preview.ID, Kind: "image", Hash: preview.FileHash,
		MimeType: preview.FileMimeType, Size: preview.FileSize, DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}
