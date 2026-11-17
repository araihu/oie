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

func TestBackupRestoreKeepsExpiredLegacyImageClaimAcrossReleasedUpgrade(t *testing.T) {
	t.Run("expired before upgrade", func(t *testing.T) {
		testBackupRestoreReleasedLegacyImageClaim(t, false)
	})
	t.Run("expired after upgrade", func(t *testing.T) {
		testBackupRestoreReleasedLegacyImageClaim(t, true)
	})
}

func testBackupRestoreReleasedLegacyImageClaim(t *testing.T, upgradeBeforeExpiry bool) {
	t.Helper()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now.AddDate(0, 0, -2))
	otherMessage := replySeed(t, source, chat, preview.URL, now, nil, nil)
	if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	moveToLegacy := func(device domain.ID) {
		t.Helper()
		if _, err := source.db.ExecContext(ctx, `UPDATE device_downloads SET device_id='legacy' WHERE device_id=?`, device); err != nil {
			t.Fatal(err)
		}
	}
	moveToLegacy(device)
	destination := copyRestoreFixture(t, source)
	images := filepath.Join(destination.dir, "media", "images")
	filename := filepath.Join(images, preview.FileHash)
	data, err := os.ReadFile(filepath.Join(source.dir, "media", "images", preview.FileHash))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(images, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	migrateFixtureToVersion(t, destination, 39)
	previews, err := testRestoreEngine(t, destination).ConversationPreviews(ctx, chat.ID, []domain.ID{otherMessage.ID})
	if err != nil || len(previews) != 1 || previews[0].Kind != "image" || previews[0].DownloadState != "not_downloaded" {
		t.Fatalf("fixture did not create a never-downloaded preview: %+v, %v", previews, err)
	}
	otherLocal := previews[0]

	// Reproduce the released Settings claim before the claim table existed.
	tx, q, err := begin(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.EnsureDownloadDevice(ctx, string(device)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CopyLegacyImageDownloads(ctx, string(device)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.DropDeviceDownloads(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := q.RemoveLegacyDownloadDevice(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	archive, database, scratch := backupRestoreFixture(t, source, true)
	if upgradeBeforeExpiry {
		destination = upgradeDownloadFixture(t, destination, 0)
		metadata, metadataDB, metadataScratch := backupRestoreFixture(t, destination, false)
		relay := openLegacyDownloadDestination(t)
		if _, err := relay.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
			t.Fatal(err)
		}
		if result, err := relay.mergeBackup(ctx, database, scratch, archive, now); err != nil || result.MediaFiles != 0 {
			t.Fatalf("metadata re-export lost the released Settings claim: %+v, %v", result, err)
		}
		host := testRestoreEngine(t, destination)
		host.SetPreviewServices(nil, mediafiles.New(images))
		if policy, err := host.SetDownloadPolicy(ctx, device, 1, false); err != nil || policy.Count != 0 || policy.Days != 1 || policy.Generation != 0 {
			t.Fatalf("current Settings did not expire the claimed image: %+v, %v", policy, err)
		}
	} else {
		// Released retention detaches the preview without a durable claim.
		if err := destination.SetDownloadDays(ctx, device, 1); err != nil {
			t.Fatal(err)
		}
		tx, q, err := begin(ctx, destination)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		removed, err := q.DropExpiredDownloads(ctx, now.UnixMilli())
		if err != nil || len(removed) != 1 || removed[0].SourceID != string(preview.ID) {
			t.Fatalf("released retention did not remove the claimed membership: %+v, %v", removed, err)
		}
		if err := detachDownloadedImage(ctx, q, removed[0].SourceID, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if policy, err := destination.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 || policy.Days != 1 || policy.Generation != 0 {
			t.Fatalf("released retention did not expire the claimed image: %+v, %v", policy, err)
		}
		if err := mediafiles.New(images).Remove(preview.FileHash); err != nil {
			t.Fatal(err)
		}
		if err := destination.CompleteDownloadGarbage(ctx, domain.DownloadGarbage{Kind: "image", Hash: preview.FileHash}, true); err != nil {
			t.Fatal(err)
		}
		// An archive of the stopped release can predate the claim migration.
		metadata, metadataDB, metadataScratch := backupRestoreFixture(t, destination, false)
		relay := openLegacyDownloadDestination(t)
		if _, err := relay.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
			t.Fatal(err)
		}
		if result, err := relay.mergeBackup(ctx, database, scratch, archive, now); err != nil || result.MediaFiles != 0 {
			t.Fatalf("old archive lost the retirement before upgrade: %+v, %v", result, err)
		}
		if _, err := os.Stat(filepath.Join(relay.dir, "media", "images", preview.FileHash)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("an expired legacy image was restored from the older archive: %v", err)
		}

		// The same archive must keep an independently retained local download.
		retained := openLegacyDownloadDestination(t)
		if result, err := retained.mergeBackup(ctx, database, scratch, archive, now); err != nil || result.MediaFiles != 1 {
			t.Fatalf("fixture did not keep the original image: %+v, %v", result, err)
		}
		if _, err := retained.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
			t.Fatal(err)
		}
		if policy, err := retained.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 1 {
			t.Fatalf("an independent local membership changed: %+v, %v", policy, err)
		}
		destination = upgradeDownloadFixture(t, destination, 0)
	}
	if err := checkRestoreDatabase(ctx, database); err != nil {
		t.Fatal(err)
	}
	if result, err := destination.mergeBackup(ctx, database, scratch, archive, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("stale membership bypassed destination retention after upgrading: %+v, %v", result, err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired image returned to disk after upgrading: %v", err)
	}

	other, otherDevice := seedBackupDownloadedImageForMessage(t, source, otherMessage, preview.URL, now)
	if other.FileHash != preview.FileHash || other.MessageID == preview.MessageID {
		t.Fatal("fixture should hold the same bytes in a separate message")
	}
	moveToLegacy(otherDevice)
	archive, database, scratch = backupRestoreFixture(t, source, true)
	if result, err := destination.mergeBackup(ctx, database, scratch, archive, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("restoring a separate image with identical bytes: %+v, %v", result, err)
	}
	if retained, err := destination.Preview(ctx, preview.ID); err != nil || retained.DownloadState != "not_downloaded" || retained.FileHash != "" {
		t.Fatalf("restoring another image revived the expired membership: %+v, %v", retained, err)
	}
	if retained, err := destination.Preview(ctx, otherLocal.ID); err != nil || retained.DownloadState != "ready" || retained.FileHash != other.FileHash {
		t.Fatalf("the never-downloaded preview could not restore its original: %+v, %v", retained, err)
	}
	if policy, err := destination.DownloadPolicy(ctx, "legacy"); err != nil || policy.Count != 1 {
		t.Fatalf("more than the separate membership was restored: %+v, %v", policy, err)
	}
}

func TestBackupRestoreKeepsCurrentNamedImageExpiryAcrossReexport(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	oldAt := now.AddDate(0, 0, -2)
	const address = "https://example.invalid/claimed.png"
	message := replySeed(t, source, chat, address, oldAt, nil, nil)
	destination := copyRestoreFixture(t, source)
	archived, staleDevice := seedBackupDownloadedImageForMessage(t, source, message, address, oldAt)
	local, device := seedBackupDownloadedImageForMessage(t, destination, message, address, oldAt)
	if local.ID == archived.ID || local.FileHash != archived.FileHash {
		t.Fatal("fixture must have independently indexed the same original")
	}
	if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", staleDevice); err != nil {
		t.Fatal(err)
	}
	host := testRestoreEngine(t, destination)
	host.SetPreviewServices(nil, mediafiles.New(filepath.Join(destination.dir, "media", "images")))
	if policy, err := host.SetDownloadPolicy(ctx, device, 1, false); err != nil || policy.Count != 0 || policy.Generation != 0 {
		t.Fatalf("Settings did not expire the named image: %+v, %v", policy, err)
	}
	metadata, metadataDB, metadataScratch := backupRestoreFixture(t, destination, false)
	relay := openLegacyDownloadDestination(t)
	if _, err := relay.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	stale, staleDB, staleScratch := backupRestoreFixture(t, source, true)
	for _, store := range []*Store{destination, relay} {
		if result, err := store.mergeBackup(ctx, staleDB, staleScratch, stale, now); err != nil || result.MediaFiles != 0 {
			t.Fatalf("stale archive revived the expired membership: %+v, %v", result, err)
		}
	}
}
