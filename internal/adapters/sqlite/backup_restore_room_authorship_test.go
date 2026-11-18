package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
)

func TestBackupRestoreKeepsVerifiedHistoricalRoomAuthorship(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		localEdit bool
	}{
		{name: "verified descendant"},
		{name: "newer local revision", localEdit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			engine.SetProtocols(&outboundProtocol{})
			room := outboundRoom(t, source, chat.AccountID, "room@rooms.localhost")
			ctx := t.Context()
			now := time.Now().UTC()
			in := application.Incoming{
				AccountID: room.AccountID, Kind: "room", Address: room.Address,
				SenderName: "Tester", ProviderID: "room:archived-historical-message",
				ArchiveID: "archive-replay", ArchiveScope: room.Address,
				OriginalText: "previously unattributed own room message", Historical: true, CreatedAt: now,
			}
			if err := engine.Receive(ctx, in); err != nil {
				t.Fatal(err)
			}
			original := correctionMessages(t, engine, room)[0]
			if original.IsOwn || original.SendState != "received" {
				t.Fatalf("historical message did not begin unattributed: %+v", original)
			}
			dest := copyRestoreFixture(t, source)
			older, olderDB, olderScratch := backupRestoreFixture(t, source, false)
			in.IsOwn, in.SenderAddress = true, "alice@localhost"
			if err := engine.Receive(ctx, in); err != nil {
				t.Fatal(err)
			}
			authenticated, err := source.Message(ctx, original.ID)
			if err != nil || !authenticated.IsOwn || authenticated.SendState != "submitted" || authenticated.RevisionID == original.RevisionID {
				t.Fatalf("authenticated MAM replay did not update the source: %+v, %v", authenticated, err)
			}
			want := authenticated
			if tc.localEdit {
				want = addBackupMessageRevision(t, dest, original, "a newer retained local body", now.Add(time.Hour))
			}
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
				t.Fatal(err)
			}
			if _, err := dest.mergeBackup(ctx, olderDB, olderScratch, older, now); err != nil {
				t.Fatal(err)
			}
			got, err := dest.Message(ctx, original.ID)
			if err != nil || !got.IsOwn || got.SendState != "submitted" || got.Sender.ID != room.AccountID ||
				got.RevisionID != want.RevisionID || got.OriginalText != want.OriginalText {
				t.Fatalf("restore lost verified ownership or the selected revision: %+v, want %+v, %v", got, want, err)
			}
			_, sequence, err := dest.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
				t.Fatal(err)
			}
			if _, repeated, err := dest.Snapshot(ctx); err != nil || repeated != sequence {
				t.Fatalf("restoring the same author proof published more events: %d to %d, %v", sequence, repeated, err)
			}
		})
	}
}
