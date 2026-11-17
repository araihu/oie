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

func TestBackupRestoreKeepsDownloadCleanupWithoutMemberships(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	_, device := seedBackupDownloadedImage(t, source, chat, now)
	oldManifest, oldArchive, oldScratch := backupRestoreFixture(t, source, true)
	if err := source.PurgeDownloads(ctx, device, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil {
		t.Fatal(err)
	}
	clearedManifest, clearedArchive, clearedScratch := backupRestoreFixture(t, source, false)

	t.Run("fresh destination", func(t *testing.T) {
		destination, err := Open(ctx, t.TempDir(), migrationID())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = destination.Close() })
		if _, err := destination.mergeBackup(ctx, clearedArchive, clearedScratch, clearedManifest, now); err != nil {
			t.Fatal(err)
		}
		result, err := destination.mergeBackup(ctx, oldArchive, oldScratch, oldManifest, now)
		if err != nil || result.MediaFiles != 0 {
			t.Fatalf("older backup revived a cleared download: %+v %v", result, err)
		}
		policy, err := destination.DownloadPolicy(ctx, device)
		if err != nil || policy.Count != 0 || policy.Generation != 1 {
			t.Fatalf("destination lost device cleanup: %+v %v", policy, err)
		}
	})

	t.Run("existing destination", func(t *testing.T) {
		destination, err := Open(ctx, t.TempDir(), migrationID())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = destination.Close() })
		if _, err := destination.mergeBackup(ctx, oldArchive, oldScratch, oldManifest, now); err != nil {
			t.Fatal(err)
		}
		if err := destination.SetDownloadDays(ctx, device, 7); err != nil {
			t.Fatal(err)
		}
		if _, err := destination.mergeBackup(ctx, clearedArchive, clearedScratch, clearedManifest, now); err != nil {
			t.Fatal(err)
		}
		policy, err := destination.DownloadPolicy(ctx, device)
		if err != nil || policy.Count != 1 || policy.Days != 7 || policy.Generation != 1 {
			t.Fatalf("backup did not preserve destination download and policy with the cleanup marker: %+v %v", policy, err)
		}
	})
}

func TestBackupRestoreKeepsLegacyDownloadsClearedBySettings(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	// Migration 35 labels pre-device image originals as legacy.
	if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.ExecContext(ctx, `UPDATE device_downloads SET device_id='legacy' WHERE device_id=?`, device); err != nil {
		t.Fatal(err)
	}
	old, oldArchive, oldScratch := backupRestoreFixture(t, source, true)

	fresh, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if result, err := fresh.mergeBackup(ctx, oldArchive, oldScratch, old, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("legacy original could not be restored to a fresh profile: %+v, %v", result, err)
	}
	engine.SetPreviewServices(nil, mediafiles.New(filepath.Join(source.dir, "media", "images")))
	if policy, err := engine.SetDownloadPolicy(ctx, device, 0, true); err != nil || policy.Count != 0 || policy.Generation != 1 {
		t.Fatalf("clearing originals from settings: %+v, %v", policy, err)
	}
	cleared, clearedArchive, clearedScratch := backupRestoreFixture(t, source, false)
	if _, err := fresh.mergeBackup(ctx, clearedArchive, clearedScratch, cleared, now); err != nil {
		t.Fatal(err)
	}
	if policy, err := fresh.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 1 {
		t.Fatalf("restored cleanup marker removed the destination's existing legacy download: %+v, %v", policy, err)
	}
	newestFirst, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = newestFirst.Close() })
	if _, err := newestFirst.mergeBackup(ctx, clearedArchive, clearedScratch, cleared, now); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		destination *Store
	}{
		{"cleared destination", source},
		{"restored cleanup marker", newestFirst},
	} {
		t.Run(tc.name, func(t *testing.T) {
			images := filepath.Join(tc.destination.dir, "media", "images")
			if result, err := tc.destination.mergeBackup(ctx, oldArchive, oldScratch, old, now); err != nil || result.MediaFiles != 0 {
				t.Fatalf("older backup revived a cleared legacy original: %+v, %v", result, err)
			}
			if _, err := os.Lstat(filepath.Join(images, preview.FileHash)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cleared original returned to disk: %v", err)
			}
			if policy, err := tc.destination.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 0 {
				t.Fatalf("cleared legacy memberships returned: %+v, %v", policy, err)
			}
		})
	}
}
