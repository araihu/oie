package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreBindsIdlessDirectImageToRetainedMessage(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	engine.SetProtocols(&outboundProtocol{})
	base := copyRestoreFixture(t, source)
	ctx := t.Context()
	origin := migrationID()
	const url = "https://example.invalid/photo.png"
	now := time.Now().UTC()
	in := application.Incoming{
		AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", IsOwn: true, CanonicalID: origin,
		OriginalText: url, CreatedAt: now, WireMentions: domain.WireMentionsAbsent,
		References: []domain.MessageReference{{Scope: "sender:alice@localhost", Author: "alice@localhost", ID: string(origin)}},
	}
	receiver := testRestoreEngine(t, base)
	receiver.SetProtocols(&outboundProtocol{})
	if err := receiver.Receive(ctx, in); err != nil {
		t.Fatal(err)
	}
	retained, err := base.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(retained) != 1 {
		t.Fatalf("did not retain the live source: %+v, %v", retained, err)
	}
	target := retained[0]

	in.ArchiveID, in.ArchiveScope, in.Historical = "source-own-archive", "alice@localhost", true
	in.ProviderID = "archive:" + bodyHash(in.ArchiveScope+"\x00"+in.ArchiveID)
	if err := engine.Receive(ctx, in); err != nil {
		t.Fatal(err)
	}
	observed, err := source.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(observed) != 1 || observed[0].ID == target.ID {
		t.Fatalf("did not retain the separate archive source: %+v, %v", observed, err)
	}
	archivedImage, device := seedBackupDownloadedImageForMessage(t, source, observed[0], url, now)
	manifest, archive, scratch := backupRestoreFixture(t, source, true)
	metadataManifest, metadataArchive, metadataScratch := backupRestoreFixture(t, source, false)
	for _, tc := range []struct {
		name         string
		cachePreview bool
	}{
		{name: "allocates a canonical preview when first restored"},
		{name: "binds to the existing canonical preview", cachePreview: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			dest := copyRestoreFixture(t, base)
			reader := testRestoreEngine(t, dest)
			var local domain.ID
			if tc.cachePreview {
				rows, err := reader.ConversationPreviews(ctx, chat.ID, []domain.ID{target.ID})
				if err != nil || len(rows) != 1 || rows[0].DownloadState == "ready" {
					t.Fatalf("expected an independently generated preview: %+v, %v", rows, err)
				}
				local = rows[0].ID
			}
			if result, err := dest.mergeBackup(ctx, metadataArchive, metadataScratch, metadataManifest, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("metadata-only merge added a duplicate or an image: %+v, %v", result, err)
			}
			if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Messages != 0 || result.MediaFiles != 1 {
				t.Fatalf("optional media was not restored on the retained message: %+v, %v", result, err)
			}
			if timeline, err := dest.Messages(ctx, chat.ID, "", 10); err != nil || len(timeline) != 1 ||
				timeline[0].ID != target.ID || timeline[0].RevisionID != target.RevisionID {
				t.Fatalf("image restore replaced or duplicated the live observation: %+v, %v", timeline, err)
			}
			previews, err := reader.ConversationPreviews(ctx, chat.ID, []domain.ID{target.ID})
			if err != nil || len(previews) != 1 || (local != "" && previews[0].ID != local) ||
				previews[0].ID == archivedImage.ID || previews[0].DownloadState != "ready" || previews[0].FileHash != archivedImage.FileHash {
				t.Fatalf("the restored image is unavailable on the visible message: %+v, %v", previews, err)
			}
			local = previews[0].ID
			if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 {
				t.Fatalf("missing or duplicate local membership: %+v, %v", policy, err)
			}
			imagePath := filepath.Join(dest.dir, "media", "images", archivedImage.FileHash)
			if _, err := os.Stat(imagePath); err != nil {
				t.Fatal(err)
			}
			reader.SetPreviewServices(nil, mediafiles.New(filepath.Dir(imagePath)))
			if _, err := reader.SetDownloadPolicy(ctx, device, 0, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(imagePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Settings did not remove the now-unused image: %v", err)
			}
			if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now.Add(time.Second)); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("stale archive undid device cleanup: %+v, %v", result, err)
			}
			if preview, err := dest.Preview(ctx, local); err != nil || preview.DownloadState != "not_downloaded" || preview.FileHash != "" {
				t.Fatalf("the cleared preview acquired old bytes: %+v, %v", preview, err)
			}
			if policy, err := dest.DownloadPolicy(ctx, device); err != nil || policy.Count != 0 || policy.Generation != 1 {
				t.Fatalf("the cleared membership reappeared: %+v, %v", policy, err)
			}
			if _, err := os.Stat(imagePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the cleared image bytes reappeared: %v", err)
			}
		})
	}
}
