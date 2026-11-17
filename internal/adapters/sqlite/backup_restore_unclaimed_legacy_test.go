package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/mediafiles"
)

func TestBackupRestoreCompletesUnclaimedLegacyMediaWithoutUndoingClear(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	preview, device := seedBackupDownloadedImage(t, source, chat, now)
	if err := source.queries.EnsureDownloadDevice(ctx, "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.ExecContext(ctx, "UPDATE device_downloads SET device_id='legacy' WHERE device_id=?", device); err != nil {
		t.Fatal(err)
	}
	initial, initialDB, initialScratch := backupRestoreFixture(t, source, true)
	relay := openLegacyDownloadDestination(t)
	if result, err := relay.mergeBackup(ctx, initialDB, initialScratch, initial, now); err != nil || result.MediaFiles != 1 {
		t.Fatalf("initial archive did not restore the unclaimed original: %+v, %v", result, err)
	}

	engine.SetPreviewServices(nil, mediafiles.New(filepath.Join(source.dir, "media", "images")))
	if policy, err := engine.SetDownloadPolicy(ctx, device, 0, true); err != nil || policy.Count != 0 || policy.Generation != 1 {
		t.Fatalf("source Settings did not clear the original: %+v, %v", policy, err)
	}
	cleared, clearedDB, clearedScratch := backupRestoreFixture(t, source, false)
	if _, err := relay.mergeBackup(ctx, clearedDB, clearedScratch, cleared, now); err != nil {
		t.Fatal(err)
	}
	if legacy, err := relay.DownloadPolicy(ctx, "legacy"); err != nil || legacy.Count != 1 {
		t.Fatalf("cleanup import removed the relay's retained original: %+v, %v", legacy, err)
	}
	metadata, metadataDB, metadataScratch := backupRestoreFixture(t, relay, false)
	full, fullDB, fullScratch := backupRestoreFixture(t, relay, true)
	partial := openLegacyDownloadDestination(t)
	if _, err := partial.mergeBackup(ctx, metadataDB, metadataScratch, metadata, now); err != nil {
		t.Fatal(err)
	}
	relayed, relayedDB, relayedScratch := backupRestoreFixture(t, partial, false)

	for _, tc := range []struct {
		name       string
		relayAgain bool
		clear      bool
	}{
		{name: "settings save"},
		{name: "relayed admission", relayAgain: true},
		{name: "local clear", clear: true},
		{name: "local clear after relay", relayAgain: true, clear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			dest := openLegacyDownloadDestination(t)
			first, db, scratch := metadata, metadataDB, metadataScratch
			if tc.relayAgain {
				first, db, scratch = relayed, relayedDB, relayedScratch
			}
			if _, err := dest.mergeBackup(ctx, db, scratch, first, now); err != nil {
				t.Fatal(err)
			}
			host := testRestoreEngine(t, dest)
			if policy, err := host.SetDownloadPolicy(ctx, device, 0, tc.clear); err != nil || policy.Count != 0 {
				t.Fatalf("unexpected device state before media arrives: %+v, %v", policy, err)
			}
			want := 1
			if tc.clear {
				want = 0
				if _, err := dest.mergeBackup(ctx, db, scratch, first, now); err != nil {
					t.Fatal(err)
				}
			}
			if result, err := dest.mergeBackup(ctx, fullDB, fullScratch, full, now); err != nil || result.MediaFiles != want {
				t.Fatalf("full archive restored the wrong media: %+v, %v", result, err)
			}
			filename := filepath.Join(dest.dir, "media", "images", preview.FileHash)
			if _, err := os.Stat(filename); (tc.clear && !errors.Is(err, os.ErrNotExist)) || (!tc.clear && err != nil) {
				t.Fatalf("unexpected state of the original bytes: %v", err)
			}
			if policy, err := host.SetDownloadPolicy(ctx, device, 0, false); err != nil || policy.Count != want {
				t.Fatalf("Settings did not claim exactly the restored originals: %+v, %v", policy, err)
			}
			if result, err := dest.mergeBackup(ctx, fullDB, fullScratch, full, now); err != nil || result.MediaFiles != 0 {
				t.Fatalf("replaying the companion archive changed restored files: %+v, %v", result, err)
			}
			if legacy, err := dest.DownloadPolicy(ctx, "legacy"); err != nil || legacy.Count != 0 {
				t.Fatalf("replaying the archive re-created the unclaimed membership: %+v, %v", legacy, err)
			}
		})
	}
}
