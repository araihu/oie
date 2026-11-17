package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreReexportsNewDownloadsOnLegacyDevice(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	original, device := seedBackupDownloadedImage(t, source, chat, now)
	early, earlyDB, earlyScratch := backupRestoreFixture(t, source, false)
	primary := openLegacyDownloadDestination(t)
	if _, err := primary.mergeBackup(ctx, earlyDB, earlyScratch, early, now); err != nil {
		t.Fatal(err)
	}

	// A queued image can finish after this device adopts an older archive's
	// cleanup generation. The completion is an actual local download.
	const pendingURL = "https://example.invalid/queued-image.png"
	message := replySeed(t, primary, chat, pendingURL, now, nil, nil)
	queued, err := primary.EnsurePreview(ctx, domain.MessagePreview{
		ID: migrationID(), RevisionID: migrationID(), AccountID: chat.AccountID, ConversationID: chat.ID,
		MessageID: message.ID, URL: pendingURL, Kind: "image", State: "ready", DownloadState: "queued", UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: queued.ID, Kind: "image", DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	clearBackupDownloads(t, source, device, now)
	redownloadBackupImage(t, source, original, device, now)
	migrateFixtureToVersion(t, source, 39)
	legacy, archive, scratch := backupRestoreFixture(t, source, true)
	if result, err := primary.mergeBackup(ctx, archive, scratch, legacy, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("older archive could not attach the existing original: %+v, %v", result, err)
	}

	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 4, 5))); err != nil {
		t.Fatal(err)
	}
	file, err := mediafiles.New(filepath.Join(primary.dir, "media", "images")).Put(ctx, imageData.Bytes(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.SaveMediaFile(ctx, file, now); err != nil {
		t.Fatal(err)
	}
	previous := queued.RevisionID
	queued.RevisionID, queued.DownloadState, queued.FileHash, queued.FileMimeType, queued.FileSize =
		migrationID(), "ready", file.Hash, file.MimeType, file.Size
	if err := primary.SaveDownloadedPreview(ctx, queued, previous, now); err != nil {
		t.Fatal(err)
	}

	const fileURL = "https://example.invalid/after-restore.pdf"
	document := replySeed(t, primary, chat, fileURL, now, nil, nil)
	ref := domain.SharedReference{ID: migrationID(), RevisionID: migrationID(), URL: fileURL, Kind: "document", FileName: "after-restore.pdf"}
	if err := primary.IndexSharedContent(ctx, []domain.SharedContentMutation{{
		Message: document, ParserVersion: application.SharedContentVersion, References: []domain.SharedReference{ref},
	}}); err != nil {
		t.Fatal(err)
	}
	data := []byte("%PDF-1.7\nnew original after legacy restore")
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	downloads := filepath.Join(primary.dir, "media", "downloads")
	if err := os.MkdirAll(downloads, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloads, hash), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := primary.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: ref.ID, SourceRevision: document.RevisionID, Kind: "file",
		Hash: hash, Size: int64(len(data)), MimeType: "application/pdf", DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	reexport, reexportDB, reexportScratch := backupRestoreFixture(t, primary, true)
	fresh := openLegacyDownloadDestination(t)
	if result, err := fresh.mergeBackup(ctx, reexportDB, reexportScratch, reexport, now); err != nil || result.MediaFiles != 3 {
		t.Fatalf("re-export lost an explicitly saved or finished original: %+v, %v", result, err)
	}
	if saved, err := fresh.DeviceDownload(ctx, device, ref.ID, document.RevisionID); err != nil || saved.Hash != hash {
		t.Fatalf("new file membership did not survive: %+v, %v", saved, err)
	}
	if got, err := fresh.Preview(ctx, queued.ID); err != nil || got.DownloadState != "ready" || got.FileHash != file.Hash {
		t.Fatalf("finished image did not survive: %+v, %v", got, err)
	}
	var proofs int
	if err := fresh.readDB.QueryRowContext(ctx, "SELECT count(*) FROM download_membership_cleanup_revisions WHERE device_id=?", device).
		Scan(&proofs); err != nil || proofs != 0 {
		t.Fatalf("downloads from an unknown cleanup acquired proofs: %d, %v", proofs, err)
	}
}

func TestBackupRestoreReexportsLegacyImagesClaimedInSettings(t *testing.T) {
	for _, tc := range []struct {
		name        string
		version     uint
		preexisting bool
	}{
		{name: "unproven cleanup", version: 39},
		{name: "known cleanup", version: 43},
		{name: "existing membership is not reclassified", version: 43, preexisting: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _, chat := outboxFixture(t)
			ctx := t.Context()
			now := time.Now().UTC()
			preview, device := seedBackupDownloadedImage(t, source, chat, now)
			primary := openLegacyDownloadDestination(t)
			wantFiles := 1
			if tc.preexisting {
				first, firstArchive, firstScratch := backupRestoreFixture(t, source, true)
				if result, err := primary.mergeBackup(ctx, firstArchive, firstScratch, first, now); err != nil || result.MediaFiles != 1 {
					t.Fatalf("could not seed the existing device membership: %+v, %v", result, err)
				}
				wantFiles = 0
			}
			// The original predates download ownership and survived a cleanup
			// of the actual device in the old profile.
			if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
				t.Fatal(err)
			}
			if _, err := source.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", device); err != nil {
				t.Fatal(err)
			}
			clearBackupDownloads(t, source, device, now)
			migrateFixtureToVersion(t, source, tc.version)
			old, oldArchive, oldScratch := backupRestoreFixture(t, source, true)
			if result, err := primary.mergeBackup(ctx, oldArchive, oldScratch, old, now); err != nil || result.MediaFiles != wantFiles {
				t.Fatalf("old archive did not restore the original: %+v, %v", result, err)
			}
			if policy, err := testRestoreEngine(t, primary).SetDownloadPolicy(ctx, device, 0, false); err != nil ||
				policy.Count != 1 || policy.Generation != 1 {
				t.Fatalf("changing device settings did not claim the retained original: %+v, %v", policy, err)
			}

			relayed, relayedArchive, relayedScratch := backupRestoreFixture(t, primary, true)
			fresh := openLegacyDownloadDestination(t)
			if result, err := fresh.mergeBackup(ctx, relayedArchive, relayedScratch, relayed, now); err != nil || result.MediaFiles != wantFiles {
				t.Fatalf("re-export restored the wrong membership after claiming legacy originals: %+v, %v", result, err)
			}
			if got, err := fresh.Preview(ctx, preview.ID); !tc.preexisting && (err != nil || got.FileHash != preview.FileHash) {
				t.Fatalf("claimed original did not survive re-export: %+v, %v", got, err)
			}
			var proofs int
			if err := fresh.readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM download_membership_cleanup_revisions WHERE device_id=?", device).
				Scan(&proofs); err != nil || proofs != 0 {
				t.Fatalf("claiming older media created cleanup proof: %d, %v", proofs, err)
			}
		})
	}
}

func TestBackupRestoreCompletesDeferredLegacyDownloadAfterRelayingMetadata(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	clearBackupDownloads(t, source, device, now)
	redownloadBackupImage(t, source, preview, device, now)
	migrateFixtureToVersion(t, source, 43)
	metadata, metadataDB, metadataScratch := backupRestoreFixture(t, source, false)
	full, fullDB, fullScratch := backupRestoreFixture(t, source, true)
	primary := openLegacyDownloadDestination(t)
	if _, err := primary.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	relay, relayDB, relayScratch := backupRestoreFixture(t, primary, false)

	for _, tc := range []struct {
		name       string
		clear      bool
		wantFiles  int
		generation int64
	}{
		{name: "untouched relay", wantFiles: 1, generation: 1},
		{name: "locally cleared relay", clear: true, generation: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			final := openLegacyDownloadDestination(t)
			if _, err := final.mergeBackup(t.Context(), relayDB, relayScratch, relay, now); err != nil {
				t.Fatal(err)
			}
			if tc.clear {
				clearBackupDownloads(t, final, device, now.Add(time.Minute))
				if _, err := final.mergeBackup(t.Context(), relayDB, relayScratch, relay, now); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := final.mergeBackup(t.Context(), fullDB, fullScratch, full, now); err != nil || result.MediaFiles != tc.wantFiles {
				t.Fatalf("companion media archive restored the wrong files: %+v, %v", result, err)
			}
			if policy, err := final.DownloadPolicy(t.Context(), device); err != nil || policy.Count != tc.wantFiles || policy.Generation != tc.generation {
				t.Fatalf("incorrect device state after relaying metadata: %+v, %v", policy, err)
			}
			if _, err := os.Stat(filepath.Join(final.dir, "media", "images", preview.FileHash)); (!tc.clear && err != nil) ||
				(tc.clear && !errors.Is(err, os.ErrNotExist)) {
				t.Fatalf("unexpected state of the deferred image bytes: %v", err)
			}
		})
	}
}
