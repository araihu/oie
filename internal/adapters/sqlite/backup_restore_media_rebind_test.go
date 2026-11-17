package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRebindsDownloadedImageAfterOpeningMessage(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	noMedia, noMediaDB, noMediaScratch := backupRestoreFixture(t, source, false)
	withMedia, withMediaDB, withMediaScratch := backupRestoreFixture(t, source, true)
	destination, err := Open(ctx, t.TempDir(), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = destination.Close() })
	if _, err := destination.mergeBackup(ctx, noMediaDB, noMediaScratch, noMedia, now); err != nil {
		t.Fatal(err)
	}
	previews, err := testRestoreEngine(t, destination).ConversationPreviews(ctx, chat.ID, []domain.ID{preview.MessageID})
	if err != nil || len(previews) != 1 || previews[0].ID == preview.ID || previews[0].DownloadState != "not_downloaded" {
		t.Fatalf("independent preview fixture: %+v %v", previews, err)
	}
	local := previews[0]
	result, err := destination.mergeBackup(ctx, withMediaDB, withMediaScratch, withMedia, now)
	if err != nil || result.MediaFiles != 1 {
		t.Fatalf("media restore did not publish the downloaded image: %+v %v", result, err)
	}
	current, err := destination.Preview(ctx, local.ID)
	if err != nil || current.DownloadState != "ready" || current.FileHash != preview.FileHash ||
		current.RevisionID == local.RevisionID || current.Title != local.Title {
		t.Fatalf("download was not rebound to the local preview: %+v %v", current, err)
	}
	if policy, err := destination.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 {
		t.Fatalf("restored original lost device membership: %+v %v", policy, err)
	}
	if _, err := os.Stat(filepath.Join(destination.dir, "media", "images", preview.FileHash)); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoreRebindsDownloadedImageFromExtensionlessURL(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	const url = "https://example.invalid/photo"
	message := replySeed(t, source, chat, url, now, nil, nil)
	downloaded, _ := seedBackupDownloadedImageForMessage(t, source, message, url, now)
	noMedia, noMediaDB, noMediaScratch := backupRestoreFixture(t, source, false)
	withMedia, withMediaDB, withMediaScratch := backupRestoreFixture(t, source, true)

	for _, previouslyResolved := range []bool{false, true} {
		name := "unresolved link"
		if previouslyResolved {
			name = "resolved link with pending refresh"
		}
		t.Run(name, func(t *testing.T) {
			destination, err := Open(ctx, t.TempDir(), migrationID())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = destination.Close() })
			if _, err := destination.mergeBackup(ctx, noMediaDB, noMediaScratch, noMedia, now); err != nil {
				t.Fatal(err)
			}
			previews, err := testRestoreEngine(t, destination).ConversationPreviews(ctx, chat.ID, []domain.ID{message.ID})
			if err != nil || len(previews) != 1 || previews[0].Kind != "link" || previews[0].State != "queued" {
				t.Fatalf("expected an unresolved local link: %+v %v", previews, err)
			}
			local := previews[0]
			if previouslyResolved {
				for _, state := range []string{"ready", "queued"} {
					parent := local.RevisionID
					local.RevisionID, local.State = migrationID(), state
					local.RefreshRequested = state == "queued"
					if err := destination.SavePreview(ctx, local, parent); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := destination.mergeBackup(ctx, withMediaDB, withMediaScratch, withMedia, now)
			if err != nil {
				t.Fatal(err)
			}
			current, err := destination.Preview(ctx, local.ID)
			if err != nil {
				t.Fatal(err)
			}
			if previouslyResolved {
				if result.MediaFiles != 0 || current.RevisionID != local.RevisionID || current.Kind != "link" {
					t.Fatalf("backup replaced a locally resolved link: %+v %+v", result, current)
				}
			} else if result.MediaFiles != 1 || current.Kind != "image" || current.State != "ready" ||
				current.DownloadState != "ready" || current.FileHash != downloaded.FileHash {
				t.Fatalf("original image did not rebind to the unresolved preview: %+v %+v", result, current)
			}
		})
	}
}

func TestBackupRestoreRebindsFileOnlyForMatchingMessageRevision(t *testing.T) {
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	const url = "https://example.invalid/report.pdf"
	message := replySeed(t, source, chat, url, now, nil, nil)
	matching, different := copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	index := func(store *Store, current domain.Message) domain.ID {
		t.Helper()
		id := migrationID()
		err := store.IndexSharedContent(ctx, []domain.SharedContentMutation{{
			Message: current, ParserVersion: application.SharedContentVersion,
			References: []domain.SharedReference{{
				ID: id, RevisionID: migrationID(), URL: url, Kind: "document", FileName: "report.pdf",
			}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sourceID := index(source, message)
	localID := index(matching, message)
	modified := addBackupMessageRevision(t, different, message, "updated text "+url, now.Add(time.Minute))
	modifiedLocalID := index(different, modified)

	const body = "%PDF-1.7\nexample file"
	checksum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(checksum[:])
	downloads := filepath.Join(source.dir, "media", "downloads")
	if err := os.MkdirAll(downloads, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloads, hash), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	device := migrationID()
	if err := source.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: sourceID, SourceRevision: message.RevisionID,
		Kind: "file", Hash: hash, MimeType: "application/pdf", Size: int64(len(body)), DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, true)
	if result, err := matching.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("file with matching message revision was not restored: %+v %v", result, err)
	}
	download, err := matching.DeviceDownload(ctx, device, localID, message.RevisionID)
	if err != nil || download.Hash != hash {
		t.Fatalf("file was not rebound to the local catalog identifier: %+v %v", download, err)
	}
	if result, err := different.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.MediaFiles != 0 {
		t.Fatalf("download for stale message revision was restored: %+v %v", result, err)
	}
	if _, err := different.DeviceDownload(ctx, device, modifiedLocalID, modified.RevisionID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("stale file became reusable for the current revision: %v", err)
	}
}
