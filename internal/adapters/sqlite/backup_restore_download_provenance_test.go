package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsProofWithEachRelayedDownload(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	oldImage, device := seedBackupDownloadedImage(t, source, chat, now)
	const url = "https://example.invalid/recovered.pdf"
	document := replySeed(t, source, chat, url, now, nil, nil)
	old, oldArchive, oldScratch := backupRestoreFixture(t, source, true)
	relay, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })
	if result, err := relay.mergeBackup(ctx, oldArchive, oldScratch, old, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("relay did not receive the original image: %+v, %v", result, err)
	}
	images := filepath.Join(source.dir, "media", "images")
	engine.SetPreviewServices(nil, mediafiles.New(images))
	if policy, err := engine.SetDownloadPolicy(ctx, device, 0, true); err != nil || policy.Count != 0 || policy.Generation != 1 {
		t.Fatalf("cleanup did not clear the local image: %+v, %v", policy, err)
	}
	oldPath := filepath.Join(images, oldImage.FileHash)
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup did not remove the image bytes: %v", err)
	}
	cleared, clearedArchive, clearedScratch := backupRestoreFixture(t, source, false)
	if _, err := relay.mergeBackup(ctx, clearedArchive, clearedScratch, cleared, now); err != nil {
		t.Fatal(err)
	}
	if policy, err := relay.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 || policy.Generation != 1 {
		t.Fatalf("merging the cleanup changed the relay's existing image: %+v, %v", policy, err)
	}
	stale, staleArchive, staleScratch := backupRestoreFixture(t, relay, true)
	if result, err := source.mergeBackup(ctx, staleArchive, staleScratch, stale, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("relay revived a pre-cleanup download: %+v, %v", result, err)
	}
	if policy, err := source.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 {
		t.Fatalf("relay restored a cleared membership: %+v, %v", policy, err)
	}

	indexDocument := func(store *Store) domain.ID {
		t.Helper()
		id := migrationID()
		if err := store.IndexSharedContent(ctx, []domain.SharedContentMutation{{
			Message: document, ParserVersion: application.SharedContentVersion,
			References: []domain.SharedReference{
				{ID: id, RevisionID: migrationID(), URL: url, Kind: "document", FileName: "recovered.pdf"},
			},
		}}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	sourceID, relayID := indexDocument(source), indexDocument(relay)
	const body = "%PDF-1.7\npost-cleanup file"
	digest := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(digest[:])
	downloads := filepath.Join(source.dir, "media", "downloads")
	if err := os.MkdirAll(downloads, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloads, hash), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: sourceID, SourceRevision: document.RevisionID, Kind: "file",
		Hash: hash, MimeType: "application/pdf", Size: int64(len(body)), DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	full, fullArchive, fullScratch := backupRestoreFixture(t, source, true)
	if result, err := relay.mergeBackup(ctx, fullArchive, fullScratch, full, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("relay did not restore the post-cleanup file: %+v, %v", result, err)
	}
	if download, err := relay.DeviceDownload(ctx, device, relayID, document.RevisionID); err != nil || download.Hash != hash {
		t.Fatalf("file did not rebind to the relay's catalog: %+v, %v", download, err)
	}
	relayed, relayedArchive, relayedScratch := backupRestoreFixture(t, relay, true)
	final, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = final.Close() })
	if _, err := final.mergeBackup(ctx, clearedArchive, clearedScratch, cleared, now); err != nil {
		t.Fatal(err)
	}
	finalID := indexDocument(final)
	if result, err := final.mergeBackup(ctx, relayedArchive, relayedScratch, relayed, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("relay lost the post-cleanup proof or admitted the old image: %+v, %v", result, err)
	}
	if download, err := final.DeviceDownload(ctx, device, finalID, document.RevisionID); err != nil || download.Hash != hash {
		t.Fatalf("post-cleanup file did not survive rebinding and re-export: %+v, %v", download, err)
	}
	if policy, err := final.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 || policy.Generation != 1 {
		t.Fatalf("relay sent the wrong memberships: %+v, %v", policy, err)
	}
	if _, err := os.Stat(filepath.Join(final.dir, "media", "images", oldImage.FileHash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleared image bytes returned through the relay: %v", err)
	}
}

func TestBackupRestoreAcceptsProvenSuccessiveCleanup(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	clearBackupDownloads(t, source, device, now)
	first, firstArchive, firstScratch := backupRestoreFixture(t, source, false)
	clearBackupDownloads(t, source, device, now.Add(time.Minute))
	clearBackupDownloads(t, source, device, now.Add(2*time.Minute))
	redownloadBackupImage(t, source, preview, device, now.Add(2*time.Minute))
	latest, latestArchive, latestScratch := backupRestoreFixture(t, source, true)
	dest, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dest.Close() })
	if _, err := dest.mergeBackup(ctx, firstArchive, firstScratch, first, now); err != nil {
		t.Fatal(err)
	}
	if result, err := dest.mergeBackup(ctx, latestArchive, latestScratch, latest, now.Add(2*time.Minute)); err != nil || result.MediaFiles != 1 {
		t.Fatalf("latest backup lost the download across successive cleanups: %+v, %v", result, err)
	}
	if _, err := dest.mergeBackup(ctx, firstArchive, firstScratch, first, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Generation != 3 || policy.Count != 1 {
		t.Fatalf("older backup regressed the cleanup head or its membership: %+v, %v", policy, err)
	}
	relayed, relayedArchive, relayedScratch := backupRestoreFixture(t, dest, true)
	final, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = final.Close() })
	if _, err := final.mergeBackup(ctx, firstArchive, firstScratch, first, now); err != nil {
		t.Fatal(err)
	}
	if result, err := final.mergeBackup(ctx, relayedArchive, relayedScratch, relayed, now.Add(2*time.Minute)); err != nil || result.MediaFiles != 1 {
		t.Fatalf("re-export lost evidence of successive cleanups: %+v, %v", result, err)
	}
}
