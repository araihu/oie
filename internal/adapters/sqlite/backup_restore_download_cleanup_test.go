package sqlite

import (
	"testing"
	"time"

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
