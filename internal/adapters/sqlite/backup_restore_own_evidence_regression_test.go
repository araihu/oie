package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreReservesOperationKeysWithLocalDeliveryEvidence(t *testing.T) {
	t.Parallel()
	for _, proof := range []string{"own archive echo", "own retraction"} {
		t.Run(proof, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			dest := copyRestoreFixture(t, source)
			ctx := t.Context()
			key := migrationID()
			composition := domain.OutgoingComposition{OriginalText: "already transmitted"}
			accepted, err := engine.SendComposition(ctx, key, chat.ID, composition, nil)
			if err != nil {
				t.Fatal(err)
			}
			pending := accepted.Message
			now := pending.CreatedAt.Add(time.Hour)
			switch proof {
			case "own archive echo":
				receiveBackupOwnDirectEcho(t, dest, chat, pending)
				// Simulate an echo retained before the engine stored a separate marker.
				if _, err := dest.db.ExecContext(ctx, "DELETE FROM restored_own_echoes WHERE message_id=?", pending.ID); err != nil {
					t.Fatal(err)
				}
			case "own retraction":
				if err := dest.RecordMessageRetraction(ctx, chat.AccountID, "direct", chat.Address, application.IncomingRetraction{
					Scope: "xmpp:correct:sender:alice@localhost", RemoteID: string(pending.ID), At: now,
				}); err != nil {
					t.Fatal(err)
				}
			}
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.PausedSends != 0 {
				t.Fatalf("restore missed local delivery evidence: %+v, %v", result, err)
			}
			_, retryErr := testRestoreEngine(t, dest).SendComposition(ctx, key, chat.ID, composition, nil)
			if tasks, err := dest.PendingMessages(ctx, now); err != nil || len(tasks) != 0 {
				t.Fatalf("retry created an executable copy: due=%d, retry=%v, pending=%v", len(tasks), retryErr, err)
			}
			if op, err := dest.Operation(ctx, key); err != nil || op.ResourceID != pending.ID {
				t.Fatalf("restore did not reserve the original operation key: %+v, %v", op, err)
			}
		})
	}
}

func TestBackupRestoreRemembersOwnEchoExpiredBeforeExport(t *testing.T) {
	t.Parallel()
	for _, queue := range []string{"already local", "from a later import"} {
		t.Run(queue, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			witness := copyRestoreFixture(t, source)
			var dest *Store
			if queue == "from a later import" {
				dest = copyRestoreFixture(t, source)
			}
			pending := queueBackupFixture(t, engine, chat.ID, "already transmitted", nil)
			if queue == "already local" {
				dest = copyRestoreFixture(t, source)
			}
			ctx := t.Context()
			now := pending.CreatedAt.Add(48 * time.Hour)
			receiveBackupOwnDirectEcho(t, witness, chat, pending)
			days := 1
			if _, err := testRestoreEngine(t, witness).SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
				t.Fatal(err)
			}
			if removed, err := witness.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || removed != 1 {
				t.Fatalf("witness did not expire the archived echo: %d, %v", removed, err)
			}
			// Older archives kept the authenticated sender reference even
			// after expiry, but had no scoped own-origin table.
			migrateFixtureToVersion(t, witness, 41)
			proof, proofDB, proofScratch := backupRestoreFixture(t, witness, false)
			if _, err := dest.mergeBackup(ctx, proofDB, proofScratch, proof, now); err != nil {
				t.Fatal(err)
			}
			if queue == "from a later import" {
				old, oldDB, oldScratch := backupRestoreFixture(t, source, false)
				if _, err := dest.mergeBackup(ctx, oldDB, oldScratch, old, now); err != nil {
					t.Fatal(err)
				}
				if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, now); err == nil {
					t.Fatal("stale send could be resumed after restoring independently expired delivery evidence")
				}
			}
			if tasks, err := dest.PendingMessages(ctx, now); err != nil || len(tasks) != 0 {
				t.Fatalf("a send observed in the archive was dispatchable: due=%d, %v", len(tasks), err)
			}
		})
	}
}

func TestBackupRestoreRemembersAuthenticatedRoomEcho(t *testing.T) {
	t.Parallel()
	source, _, placeholder := outboxFixture(t)
	engine, room := promoteBackupRoomFixture(t, source, placeholder)
	witness := copyRestoreFixture(t, source)
	dest := copyRestoreFixture(t, source)
	ctx := t.Context()
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	pending := queueBackupFixture(t, engine, room.ID, "already transmitted to the room", nil)
	old, oldDB, oldScratch := backupRestoreFixture(t, source, false)
	now := pending.CreatedAt.Add(time.Hour)
	// The room adapter keeps the origin ID separate from its scoped provider
	// ID and verifies own authorship only when the archive supplies a real JID.
	if err := witnessEngine.Receive(ctx, application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		IsOwn: true, SenderAddress: "alice@localhost", SenderName: "Tester",
		ProviderID:  "room:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CanonicalID: pending.ID, ArchiveID: "known-room-archive-echo", ArchiveScope: room.Address,
		OriginalText: pending.OriginalText, CreatedAt: pending.CreatedAt, Historical: true,
	}); err != nil {
		t.Fatal(err)
	}
	proof, proofDB, proofScratch := backupRestoreFixture(t, witness, false)
	if _, err := dest.mergeBackup(ctx, proofDB, proofScratch, proof, now); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(ctx, oldDB, oldScratch, old, now); err != nil {
		t.Fatal(err)
	}
	if err := dest.ReviewRecoveredSend(ctx, pending.ID, pending.RevisionID, true, now); err == nil {
		t.Fatal("stale room send could be resumed after restoring its authenticated own echo")
	}
}

func receiveBackupOwnDirectEcho(t *testing.T, store *Store, chat domain.Conversation, message domain.Message) {
	t.Helper()
	const address = "alice@localhost"
	engine := testRestoreEngine(t, store)
	engine.SetProtocols(&outboundProtocol{})
	if err := engine.Receive(t.Context(), application.Incoming{
		AccountID: chat.AccountID, Kind: "direct", Address: chat.Address,
		IsOwn: true, ProviderID: string(message.ID), ArchiveID: "verified-direct-archive-echo",
		ArchiveScope: address, OriginalText: message.OriginalText, CreatedAt: message.CreatedAt, Historical: true,
		References: []domain.MessageReference{{Scope: "sender:" + address, Author: address, ID: string(message.ID)}},
	}); err != nil {
		t.Fatal(err)
	}
}
