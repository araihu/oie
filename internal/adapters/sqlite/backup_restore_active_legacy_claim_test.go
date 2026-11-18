package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsActiveNamedImageEvidenceWithoutMedia(t *testing.T) {
	t.Parallel()
	legacy, named, old, current, namedDevice, now := independentlyDownloadedLegacyImage(t)
	ctx := t.Context()
	if policy, err := testRestoreEngine(t, named).SetDownloadPolicy(ctx, namedDevice, 1, false); err != nil || policy.Days != 1 || policy.Count != 1 {
		t.Fatalf("saving Settings did not retain the fresh named image: %+v, %v", policy, err)
	}
	stale, staleDB, staleScratch := backupRestoreFixture(t, legacy, true)
	if result, err := named.mergeBackup(ctx, staleDB, staleScratch, stale, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("the named profile imported its older global copy: %+v, %v", result, err)
	}
	if policy, err := named.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 0 {
		t.Fatalf("the named profile accepted an extra global membership: %+v, %v", policy, err)
	}
	for _, tc := range []struct {
		name    string
		version uint
	}{
		{name: "current archive"},
		{name: "released archive", version: 39},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if tc.version != 0 {
				migrateFixtureToVersion(t, named, tc.version)
			}
			metadata, db, scratch := backupRestoreFixture(t, named, false)
			relay := openLegacyDownloadDestination(t)
			if result, err := relay.mergeBackup(ctx, db, scratch, metadata, now); err != nil || result.MediaFiles != 0 {
				t.Fatalf("restoring named image metadata: %+v, %v", result, err)
			}
			for range 2 {
				if result, err := relay.mergeBackup(ctx, staleDB, staleScratch, stale, now); err != nil || result.MediaFiles != 0 {
					t.Fatalf("metadata restore lost proof against the older unclaimed membership: %+v, %v", result, err)
				}
			}
			if _, err := os.Stat(filepath.Join(relay.dir, "media", "images", old.FileHash)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the old global original returned to disk: %v", err)
			}
			full, fullDB, fullScratch := backupRestoreFixture(t, named, true)
			if result, err := relay.mergeBackup(ctx, fullDB, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
				t.Fatalf("named evidence prevented its own full companion: %+v, %v", result, err)
			}
			if got, err := relay.Preview(ctx, current.ID); err != nil || got.FileHash != current.FileHash {
				t.Fatalf("the explicitly named original did not restore: %+v, %v", got, err)
			}
			if policy, err := relay.DownloadPolicy(ctx, namedDevice); err != nil || policy.Count != 1 {
				t.Fatalf("the named membership was lost: %+v, %v", policy, err)
			}
			if policy, err := relay.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 0 {
				t.Fatalf("the stale global membership was restored: %+v, %v", policy, err)
			}
		})
	}
}

func TestBackupRestoreKeepsGlobalAndNamedImageMembershipChoices(t *testing.T) {
	t.Parallel()
	legacy, named, old, _, namedDevice, now := independentlyDownloadedLegacyImage(t)
	ctx := t.Context()
	older, olderDB, olderScratch := backupRestoreFixture(t, legacy, true)
	metadata, metadataDB, metadataScratch := backupRestoreFixture(t, named, false)
	local := openLegacyDownloadDestination(t)
	if result, err := local.mergeBackup(ctx, olderDB, olderScratch, older, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("restoring the local global original: %+v, %v", result, err)
	}
	if _, err := local.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	if policy, err := local.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 1 {
		t.Fatalf("the named source overrode the local global membership: %+v, %v", policy, err)
	}
	if err := local.SaveDownload(ctx, domain.Download{
		DeviceID: namedDevice, SourceID: old.ID, Kind: "image", Hash: old.FileHash,
		MimeType: old.FileMimeType, Size: old.FileSize, DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	both, bothDB, bothScratch := backupRestoreFixture(t, local, false)
	full, fullDB, fullScratch := backupRestoreFixture(t, local, true)
	relay := openLegacyDownloadDestination(t)
	if _, err := relay.mergeBackup(ctx, bothDB, bothScratch, both, now); err != nil {
		t.Fatal(err)
	}
	if result, err := relay.mergeBackup(ctx, fullDB, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("relaying the two retained image memberships: %+v, %v", result, err)
	}
	for _, device := range []domain.ID{"legacy", namedDevice} {
		if policy, err := relay.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 {
			t.Fatalf("the re-export lost the retained membership for %s: %+v, %v", device, policy, err)
		}
	}
}

func independentlyDownloadedLegacyImage(t *testing.T) (legacy, named *Store, old, current domain.MessagePreview, namedDevice domain.ID, now time.Time) {
	t.Helper()
	legacy, _, chat := outboxFixture(t)
	ctx := t.Context()
	now = time.Now().UTC()
	oldAt := now.AddDate(0, 0, -2)
	const url = "https://example.invalid/known-image.png"
	message := replySeed(t, legacy, chat, url, oldAt, nil, nil)
	named = copyRestoreFixture(t, legacy)
	old, oldDevice := seedBackupDownloadedImageForMessage(t, legacy, message, url, oldAt)
	if err := legacy.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", oldDevice); err != nil {
		t.Fatal(err)
	}
	current, namedDevice = seedBackupDownloadedImageForMessage(t, named, message, url, now)
	if old.ID == current.ID || old.FileHash != current.FileHash {
		t.Fatal("the two profiles should have independently downloaded the same original")
	}
	return legacy, named, old, current, namedDevice, now
}
