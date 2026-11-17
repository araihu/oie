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

func TestBackupRestoreWithoutMediaRecreatesDownloadableImagePreview(t *testing.T) {
	source, _, chat := outboxFixture(t)
	now := time.Now().UTC()
	preview, _ := seedBackupDownloadedImage(t, source, chat, now)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	ctx := t.Context()
	destination, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("merge without media: %+v, %v", result, err)
	}
	engine := testRestoreEngine(t, destination)
	previews, err := engine.ConversationPreviews(ctx, chat.ID, []domain.ID{preview.MessageID})
	if err != nil || len(previews) != 1 || previews[0].Kind != "image" || previews[0].DownloadState != "not_downloaded" || previews[0].FileHash != "" {
		t.Fatalf("preview cannot be downloaded again: %+v, %v", previews, err)
	}
	if _, err := destination.MediaFile(ctx, preview.FileHash); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("missing bytes registered as media: %v", err)
	}
}

func TestBackupRestoreWithMediaLeavesDeletedDownloadsDeleted(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	manifest, archive, scratch := backupRestoreFixture(t, source, true)

	t.Run("fresh profile gets retained download", func(t *testing.T) {
		destination, err := Open(t.Context(), t.TempDir(), migrationID())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = destination.Close() })
		if result, err := destination.mergeBackup(t.Context(), archive, scratch, manifest, now); err != nil || result.MediaFiles != 1 {
			t.Fatalf("merge with media: %+v, %v", result, err)
		}
		p, err := destination.Preview(t.Context(), preview.ID)
		if err != nil || p.DownloadState != "ready" || p.FileHash != preview.FileHash {
			t.Fatalf("downloaded preview was lost: %+v, %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(destination.dir, "media", "images", preview.FileHash)); err != nil {
			t.Fatalf("valid media was not published: %v", err)
		}
	})

	destination := copyRestoreFixture(t, source)
	if err := destination.PurgeDownloads(ctx, device, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil {
		t.Fatal(err)
	}
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("merge after delete: %+v, %v", result, err)
	}
	policy, err := destination.DownloadPolicy(ctx, device)
	if err != nil || policy.Generation != 1 || policy.Count != 0 {
		t.Fatalf("deleted download was imported: %+v, %v", policy, err)
	}
	p, err := destination.Preview(ctx, preview.ID)
	if err != nil || p.DownloadState != "not_downloaded" || p.FileHash != "" {
		t.Fatalf("destination deletion was reverted: %+v, %v", p, err)
	}
	if _, err := os.Lstat(filepath.Join(destination.dir, "media", "images", preview.FileHash)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old original was published again: %v", err)
	}

	// Both copies can reach generation 1 independently. Downloading again in
	// the source must not cancel the destination's deletion.
	if err := source.PurgeDownloads(ctx, device, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil {
		t.Fatal(err)
	}
	current, err := source.Preview(ctx, preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview.RevisionID = migrationID()
	if err := source.SavePreview(ctx, preview, current.RevisionID); err != nil {
		t.Fatal(err)
	}
	if err := source.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: preview.ID, Kind: "image", Hash: preview.FileHash, MimeType: preview.FileMimeType,
		Size: preview.FileSize, DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch = backupRestoreFixture(t, source, true)
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("merge of independently purged devices: %+v, %v", result, err)
	}
	if policy, err := destination.DownloadPolicy(ctx, device); err != nil || policy.Generation != 1 || policy.Count != 0 {
		t.Fatalf("equal generations revived a deleted membership: %+v, %v", policy, err)
	}
}

func TestBackupRestoreHonorsDestinationDownloadRetention(t *testing.T) {
	source, _, chat := outboxFixture(t)
	destination := copyRestoreFixture(t, source)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now.AddDate(0, 0, -2))
	manifest, archive, scratch := backupRestoreFixture(t, source, true)
	if err := destination.SetDownloadDays(ctx, device, 1); err != nil {
		t.Fatal(err)
	}
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("merge after expiry: %+v, %v", result, err)
	}
	if _, err := destination.Preview(ctx, preview.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("expired original still appears as a preview: %v", err)
	}
	if policy, err := destination.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 || policy.Days != 1 {
		t.Fatalf("destination expiry did not apply: %+v, %v", policy, err)
	}
}

func seedBackupDownloadedImage(t *testing.T, store *Store, chat domain.Conversation, now time.Time) (domain.MessagePreview, domain.ID) {
	t.Helper()
	ctx := t.Context()
	const source = "https://example.invalid/photo.png"
	message := replySeed(t, store, chat, source, now, nil, nil)
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	file, err := mediafiles.New(filepath.Join(store.dir, "media", "images")).Put(ctx, imageBytes.Bytes(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMediaFile(ctx, file, now); err != nil {
		t.Fatal(err)
	}
	p, err := store.EnsurePreview(ctx, domain.MessagePreview{
		ID: migrationID(), RevisionID: migrationID(), AccountID: chat.AccountID, ConversationID: chat.ID,
		MessageID: message.ID, URL: source, Kind: "image", State: "ready", DownloadState: "ready",
		FileHash: file.Hash, FileMimeType: file.MimeType, FileSize: file.Size, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	device := migrationID()
	if err := store.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: p.ID, Kind: "image", Hash: file.Hash, MimeType: file.MimeType,
		Size: file.Size, DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return p, device
}

func TestOpenReconcilesInterruptedRestoreMedia(t *testing.T) {
	store, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, _ := seedBackupDownloadedImage(t, store, chat, now)
	retained := backupFile{
		Name: "media/images/" + preview.FileHash, Size: preview.FileSize, SHA256: preview.FileHash,
	}
	orphanBytes := []byte("uncommitted photo")
	sha := sha256.Sum256(orphanBytes)
	orphanHash := hex.EncodeToString(sha[:])
	orphan := backupFile{
		Name: "media/images/" + orphanHash, Size: int64(len(orphanBytes)), SHA256: orphanHash,
	}
	// An unreferenced media metadata row must not keep an orphan on disk.
	if err := store.SaveMediaFile(ctx, domain.MediaFile{
		Hash: orphanHash, Size: orphan.Size, MimeType: "image/png",
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.writeRestoreMediaJournal([]backupFile{orphan, retained}); err != nil {
		t.Fatal(err)
	}
	orphanPath := filepath.Join(store.dir, filepath.FromSlash(orphan.Name))
	partialPath := restoreMediaStagingPath(orphanPath)
	for _, name := range []string{orphanPath, partialPath} {
		if err := os.WriteFile(name, orphanBytes, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, store.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	// Reopening simulates the next engine start after a process interruption
	// on either side of the database commit.
	for _, name := range []string{orphanPath, partialPath, filepath.Join(store.dir, "private", restoreMediaJournalName)} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("interrupted restore file %s remains: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(store.dir, filepath.FromSlash(retained.Name))); err != nil {
		t.Fatalf("committed file was removed: %v", err)
	}
}
