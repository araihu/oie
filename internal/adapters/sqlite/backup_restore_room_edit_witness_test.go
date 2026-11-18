package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type roomEditWitnessProtocol struct {
	outboundProtocol
	scope, author string
	intent        domain.ID
}

func (p *roomEditWitnessProtocol) RoomSession(_ context.Context, a domain.Account, c domain.Conversation) (domain.RoomSession, error) {
	return domain.RoomSession{AccountID: a.ID, ConversationID: c.ID, State: "joined", Active: true, IntentRevisionID: p.intent}, nil
}

func (p *roomEditWitnessProtocol) PrepareCorrection(_ context.Context, _ domain.Account, _ domain.Conversation, m domain.Message) (domain.OutgoingCorrection, error) {
	return domain.OutgoingCorrection{
		TargetMessageID: m.ID, OriginalWireID: m.ProviderID, Scope: p.scope, Author: p.author,
		SessionID: "fixture-session", RoomIntentRevisionID: p.intent,
	}, nil
}

func TestBackupRestoreKeepsRoomEditWitnessAndAcceptedDescendant(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source, editor, placeholder := outboxFixture(t)
	room := outboundRoom(t, source, placeholder.AccountID, "group@rooms.localhost")
	author := room.Address + "/Tester"
	scope := fmt.Sprintf("xmpp:correct:room:%s:%x", room.Address, sha256.Sum256([]byte(author+"\x00"+"own-occupant-id")))
	protocol := &roomEditWitnessProtocol{scope: scope, author: author}
	editor.SetProtocols(protocol)
	joined, err := editor.JoinRoom(ctx, migrationID(), room.ID, "Tester", "")
	if err != nil {
		t.Fatal(err)
	}
	protocol.intent = joined.Session.IntentRevisionID
	original := ownSubmittedEdit(t, source, editor, room, "the editable room original")
	witnessed := copyRestoreFixture(t, source)
	first := queuedEdit(t, editor, original.ID, "the first verified correction")
	finishEdit(t, source, first.Command.ID, "submitted")
	child := queuedEdit(t, editor, original.ID, "the second correction accepted on the native revision")
	finishEdit(t, source, child.Command.ID, "submitted")

	receiver := testRestoreEngine(t, witnessed)
	receiver.SetProtocols(&outboundProtocol{})
	now := time.Now().UTC()
	observe := func(id, target domain.ID, text, key string) {
		t.Helper()
		// XMPP MAM scopes a room provider key to the full occupant JID and
		// independently proves correction authority with the occupant ID.
		provider := fmt.Sprintf("room:%x", sha256.Sum256([]byte(author+"\x00"+string(id))))
		if err := receiver.Receive(ctx, application.Incoming{
			AccountID: room.AccountID, Address: room.Address, Kind: "room", IsOwn: true, Historical: true,
			CanonicalID: id, ProviderID: provider, SenderName: "Tester", SenderAddress: "alice@localhost",
			CreatedAt: now, OriginalText: text, ArchiveScope: room.Address, ArchiveID: key,
			WireMentions: domain.WireMentionsAbsent,
			Correction:   &domain.MessageCorrection{ID: string(id), TargetID: string(target), Scope: scope, Author: author},
		}); err != nil {
			t.Fatal(err)
		}
	}
	observe(original.ID, "", original.OriginalText, "observed-original")
	const correctionKey = "observed-correction"
	observe(first.Command.ID, original.ID, first.Command.OriginalText, correctionKey)
	alias := dbgen.GetArchiveAliasParams{AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: correctionKey}
	witnessID, err := witnessed.queries.GetArchiveAlias(ctx, alias)
	if err != nil || witnessID == string(first.Command.ID) {
		t.Fatalf("fixture did not retain an independent room witness: %q, %v", witnessID, err)
	}
	witness, err := witnessed.MessageSource(ctx, domain.ID(witnessID))
	if err != nil {
		t.Fatal(err)
	}

	checkTimeline := func(t *testing.T, store *Store, text string) {
		t.Helper()
		rows, err := store.Messages(ctx, room.ID, "", 10)
		if err != nil || len(rows) != 1 || rows[0].ID != original.ID || !rows[0].Edited || rows[0].OriginalText != text {
			t.Fatalf("the room correction chain no longer projects one edited message: %+v, %v", rows, err)
		}
	}
	checkTimeline(t, witnessed, first.Command.OriginalText)
	witnessManifest, witnessArchive, witnessScratch := backupRestoreFixture(t, witnessed, false)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	checkMerged := func(t *testing.T, store *Store, retained domain.Message) {
		t.Helper()
		checkTimeline(t, store, child.Command.OriginalText)
		engine := testRestoreEngine(t, store)
		for _, command := range []domain.MessageEditCommand{first.Command, child.Command} {
			replay, err := engine.EditMessage(ctx, command.ID, original.ID, command.ExpectedRevisionID, command.OriginalText)
			if err != nil || !replay.Replayed || replay.Command.State != "submitted" {
				t.Fatalf("the accepted room correction no longer replays: %+v, %v", replay, err)
			}
		}
		if owner, err := store.queries.GetArchiveAlias(ctx, alias); err != nil || owner != string(retained.ID) {
			t.Fatalf("the room archive key changed its retained source: %q, %v", owner, err)
		}
		if raw, err := store.MessageSource(ctx, retained.ID); err != nil || raw.RevisionID != retained.RevisionID || raw.ProviderID != retained.ProviderID {
			t.Fatalf("the retained room witness lost its revision or provider key: %+v, %v", raw, err)
		}
		history := editHistory(t, engine, original.ID, "", 10)
		acceptedParent := slices.ContainsFunc(history.Items, func(item domain.MessageEditHistoryItem) bool {
			return item.IsCurrent && item.SourceMessageID == child.Command.ID &&
				len(item.ParentRevisionIDs) == 1 && item.ParentRevisionIDs[0] == retained.RevisionID
		})
		if len(history.Items) != 3 || !acceptedParent {
			t.Fatalf("the child correction lost its verified retained parent: %+v", history)
		}
	}

	t.Run("witness then native then relay", func(t *testing.T) {
		assertLocalEditRestoreRepeats(t, witnessed, manifest, archive, scratch, now)
		checkMerged(t, witnessed, witness)
		relayManifest, relayArchive, relayScratch := backupRestoreFixture(t, witnessed, false)
		relay := openLegacyDownloadDestination(t)
		assertLocalEditRestoreRepeats(t, relay, relayManifest, relayArchive, relayScratch, now)
		checkMerged(t, relay, witness)
		assertLocalEditRestoreRepeats(t, relay, manifest, archive, scratch, now)
		checkMerged(t, relay, witness)
	})
	t.Run("native then witness", func(t *testing.T) {
		native := openLegacyDownloadDestination(t)
		assertLocalEditRestoreRepeats(t, native, manifest, archive, scratch, now)
		firstSource, err := native.MessageSource(ctx, first.Command.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertLocalEditRestoreRepeats(t, native, witnessManifest, witnessArchive, witnessScratch, now)
		checkMerged(t, native, firstSource)
	})
}
