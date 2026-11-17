package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsVerifiedReplyReferencesFromOlderRoomEchoes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		attachment  bool
		priorUpload bool
	}{
		{name: "plain text"},
		{name: "retained attachment wire", attachment: true},
		{name: "terminal attachment wire from another upload", attachment: true, priorUpload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dest, _, placeholder := outboxFixture(t)
			destEngine, room := promoteBackupRoomFixture(t, dest, placeholder)
			witness := copyRestoreFixture(t, dest)
			witnessEngine := testRestoreEngine(t, witness)
			witnessEngine.SetProtocols(&outboundProtocol{})
			ctx := t.Context()

			const caption = "our old retained message"
			composition := domain.OutgoingComposition{OriginalText: caption}
			wire := caption
			if tc.attachment {
				destEngine.SetProtocols(&restoreAttachmentProtocol{})
				file, _, err := destEngine.StageAttachment(ctx, room.ID, migrationID(), "retained.txt", "text/plain", strings.NewReader("attached bytes"))
				if err != nil {
					t.Fatal(err)
				}
				composition.AttachmentIDs = []domain.ID{file.ID}
				wire = caption + "\nhttps://example.invalid/upload/retained.txt"
			}
			accepted, err := destEngine.SendComposition(ctx, migrationID(), room.ID, composition, nil)
			if err != nil {
				t.Fatal(err)
			}
			pending := accepted.Message
			oldAt := pending.CreatedAt.Add(time.Minute)
			finish := func(store *Store, sentWire, state string) {
				t.Helper()
				intent, err := store.queries.GetRoomIntent(ctx, string(room.ID))
				if err != nil {
					t.Fatal(err)
				}
				claim, err := store.ClaimDispatchMessage(ctx, pending.ID, pending.RevisionID, domain.ID(intent.RevisionID), migrationID(), oldAt, true)
				if err != nil {
					t.Fatal(err)
				}
				if !tc.attachment {
					sentWire = ""
				}
				if err := store.RecordOriginalWireAttempt(ctx, claim.ID, claim.RevisionID, application.Outgoing{OriginalText: caption}, sentWire); err != nil {
					t.Fatal(err)
				}
				if _, err := store.TransitionMessage(ctx, pending.ID, "dispatching", state, migrationID(), oldAt, oldAt); err != nil {
					t.Fatal(err)
				}
			}
			if tc.priorUpload {
				fork := copyRestoreFixture(t, dest)
				finish(fork, wire, "submitted")
				finish(dest, caption+"\nhttps://example.invalid/upload/local-attempt.txt", "queued")
				proof, proofDB, proofScratch := backupRestoreFixture(t, fork, false)
				if _, err := dest.mergeBackup(ctx, proofDB, proofScratch, proof, oldAt); err != nil {
					t.Fatal(err)
				}
			} else {
				finish(dest, wire, "submitted")
			}
			now := oldAt.AddDate(0, 0, 2)
			days := 1
			if _, err := destEngine.SetConversationRetention(ctx, room.ID, "0", &days); err != nil {
				t.Fatal(err)
			}
			if n, err := dest.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || n != 0 {
				t.Fatalf("room minimum did not preserve the destination original: %d, %v", n, err)
			}

			ref := domain.MessageReference{Scope: "room:" + room.Address, Author: room.Address + "/Tester", ID: "old-room-stanza"}
			const archiveID = "old-verified-own-echo"
			if err := witnessEngine.Receive(ctx, application.Incoming{
				AccountID: room.AccountID, Kind: "room", Address: room.Address,
				IsOwn: true, SenderAddress: "alice@localhost", SenderName: "Tester",
				ProviderID: "room:old-echo-provider", CanonicalID: pending.ID, ArchiveID: archiveID, ArchiveScope: room.Address,
				OriginalText: wire, CreatedAt: oldAt, Historical: true, References: []domain.MessageReference{ref},
			}); err != nil {
				t.Fatal(err)
			}
			aliasKey := dbgen.GetArchiveAliasParams{
				AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: archiveID,
			}
			echoID, err := witness.queries.GetArchiveAlias(ctx, aliasKey)
			if err != nil {
				t.Fatal(err)
			}
			quote := "> " + caption + "\n"
			reply := replySeed(t, witness, room, quote+"the recent answer", now, nil, &domain.ReplyReference{
				Target: ref, Fallback: []domain.TextRange{{End: len(quote)}},
			})
			manifest, archive, scratch := backupRestoreFixture(t, witness, false)
			if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Messages != 1 {
				t.Fatalf("restore should import only the recent message body: %+v, %v", result, err)
			}
			if _, err := dest.Message(ctx, domain.ID(echoID)); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("archive body outside retention was imported: %v", err)
			}
			got, err := dest.Message(ctx, reply.ID)
			if err != nil || got.Reply == nil || !got.Reply.Available || got.Reply.OriginalExpired ||
				got.Reply.MessageID != pending.ID || got.OriginalText != reply.OriginalText {
				t.Fatalf("recent reply lost its retained target: %+v, %v", got, err)
			}
			if alias, err := dest.queries.GetArchiveAlias(ctx, aliasKey); err != nil || alias != string(pending.ID) {
				t.Fatalf("old echo alias did not resolve to the retained original: %s, %v", alias, err)
			}
			if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
				t.Fatalf("repeating restore changed retained history: %+v, %v", result, err)
			}
		})
	}
}
