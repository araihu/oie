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

func TestBackupRestoreBindsIdlessDirectFileToRetainedCatalog(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source, sender, chat := outboxFixture(t)
	sender.SetProtocols(&outboundProtocol{})
	base := copyRestoreFixture(t, source)
	receiver := testRestoreEngine(t, base)
	receiver.SetProtocols(&outboundProtocol{})
	origin := migrationID()
	const url, name = "https://example.invalid/report.pdf", "report.pdf"
	now := time.Now().UTC()
	ref := domain.SharedReference{URL: url, Kind: "document", FileName: name}
	incoming := application.Incoming{
		AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", IsOwn: true, CanonicalID: origin,
		OriginalText: url, CreatedAt: now, WireMentions: domain.WireMentionsAbsent, FileReferences: []domain.SharedReference{ref},
		References: []domain.MessageReference{{Scope: "sender:alice@localhost", Author: "alice@localhost", ID: string(origin)}},
	}
	if err := receiver.Receive(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	incoming.ArchiveID, incoming.ArchiveScope, incoming.Historical = "archived-own-file", "alice@localhost", true
	incoming.ProviderID = "archive:" + bodyHash(incoming.ArchiveScope+"\x00"+incoming.ArchiveID)
	if err := sender.Receive(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	message := func(store *Store) domain.Message {
		t.Helper()
		rows, err := store.Messages(ctx, chat.ID, "", 10)
		if err != nil || len(rows) != 1 {
			t.Fatalf("expected one visible direct observation: %+v, %v", rows, err)
		}
		return rows[0]
	}
	live, witnessed := message(base), message(source)
	if live.ID == witnessed.ID || live.RevisionID == witnessed.RevisionID {
		t.Fatal("fixture needs independent observations")
	}
	index := func(store *Store, current domain.Message) domain.SharedContentItem {
		t.Helper()
		entry := ref
		entry.ID, entry.RevisionID = migrationID(), migrationID()
		if err := store.IndexSharedContent(ctx, []domain.SharedContentMutation{{
			Message: current, ParserVersion: application.SharedContentVersion, References: []domain.SharedReference{entry},
		}}); err != nil {
			t.Fatal(err)
		}
		page, err := testRestoreEngine(t, store).SharedContent(ctx, domain.SharedContentQuery{
			ConversationID: chat.ID, Type: "documents", MessageIDs: []domain.ID{current.ID}, Limit: 10,
		})
		if err != nil || len(page.Items) != 1 || page.Items[0].URL != url {
			t.Fatalf("expected the visible PDF catalog item: %+v, %v", page, err)
		}
		return page.Items[0]
	}
	archived := index(source, witnessed)
	reverseBase := copyRestoreFixture(t, source)
	const body = "%PDF-1.7\nverified shared file"
	hash := bodyHash(body)
	filePath := func(store *Store) string { return filepath.Join(store.dir, "media", "downloads", hash) }
	if err := os.MkdirAll(filepath.Dir(filePath(source)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath(source), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	device := migrationID()
	if err := source.SaveDownload(ctx, domain.Download{
		DeviceID: device, SourceID: archived.ID, SourceRevision: witnessed.RevisionID, Kind: "file", Hash: hash,
		MimeType: "application/pdf", Size: int64(len(body)), DownloadedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, true)
	metadata, metadataDB, metadataDir := backupRestoreFixture(t, source, false)
	check := func(store *Store, current domain.Message) domain.ID {
		t.Helper()
		if got := message(store); got.ID != current.ID || got.RevisionID != current.RevisionID {
			t.Fatalf("restore changed the visible file carrier: %+v", got)
		}
		item := index(store, current)
		download, err := store.DeviceDownload(ctx, device, item.ID, current.RevisionID)
		if err != nil || download.Hash != hash || download.Size != int64(len(body)) {
			t.Fatalf("verified bytes were not bound to the visible catalog item: %+v, %v", download, err)
		}
		if policy, err := store.DownloadPolicy(ctx, device); err != nil || policy.Count != 1 {
			t.Fatalf("restore created a hidden or duplicate membership: %+v, %v", policy, err)
		}
		if restored, err := os.ReadFile(filePath(store)); err != nil || string(restored) != body {
			t.Fatalf("the retained media file is unavailable: %v", err)
		}
		return item.ID
	}
	for _, tc := range []struct {
		name     string
		preindex bool
	}{
		{name: "reserve an unindexed local entry"},
		{name: "keep an existing catalog ID", preindex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := copyRestoreFixture(t, base)
			var before domain.ID
			if tc.preindex {
				before = index(destination, live).ID
			}
			if result, err := destination.mergeBackup(ctx, metadataDB, metadataDir, metadata, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("metadata-only restore altered media or history counts: %+v, %v", result, err)
			}
			if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Messages != 0 || result.MediaFiles != 1 {
				t.Fatalf("full restore did not fill the canonical file: %+v, %v", result, err)
			}
			localID := check(destination, live)
			if localID == archived.ID || (before != "" && localID != before) {
				t.Fatalf("restore replaced the local catalog identity: got %s, previous %s, archive %s", localID, before, archived.ID)
			}
			relayManifest, relayDB, relayScratch := backupRestoreFixture(t, destination, true)
			fresh := openLegacyDownloadDestination(t)
			assertLocalEditRestoreRepeats(t, fresh, relayManifest, relayDB, relayScratch, now)
			if id := check(fresh, live); id != localID {
				t.Fatalf("reexport changed the canonical catalog ID: %s", id)
			}
			reverse := copyRestoreFixture(t, reverseBase)
			assertLocalEditRestoreRepeats(t, reverse, relayManifest, relayDB, relayScratch, now)
			if id := check(reverse, witnessed); id != archived.ID {
				t.Fatalf("reverse restore changed the existing catalog ID: %s", id)
			}

			settings := testRestoreEngine(t, destination)
			settings.SetDownloadFiles(mediafiles.NewDownloads(filepath.Dir(filePath(destination))))
			if policy, err := settings.SetDownloadPolicy(ctx, device, 0, true); err != nil || policy.Count != 0 || policy.Generation != 1 {
				t.Fatalf("clear did not retire the visible membership: %+v, %v", policy, err)
			}
			for _, previous := range []struct {
				manifest backupManifest
				db, dir  string
			}{{manifest, archive, scratch}, {relayManifest, relayDB, relayScratch}} {
				if result, err := destination.mergeBackup(ctx, previous.db, previous.dir, previous.manifest, now); err != nil || result != (RestoreSummary{}) {
					t.Fatalf("stale media restore undid the explicit clear: %+v, %v", result, err)
				}
			}
			if _, err := destination.DeviceDownload(ctx, device, localID, live.RevisionID); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("the cleared file became available again: %v", err)
			}
			if _, err := os.Stat(filePath(destination)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("the cleared bytes reappeared: %v", err)
			}
		})
	}
}
