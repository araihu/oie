package sqlite

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreOlderRoomEchoPreservesExistingReferenceOwner(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sameArchiveAlias bool
	}{
		{name: "archive alias", sameArchiveAlias: true},
		{name: "scoped room reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testBackupRestoreOlderRoomEchoPreservesExistingReferenceOwner(t, tc.sameArchiveAlias)
		})
	}
}

func testBackupRestoreOlderRoomEchoPreservesExistingReferenceOwner(t *testing.T, sameArchiveAlias bool) {
	t.Helper()
	dest, _, placeholder := outboxFixture(t)
	engine, room := promoteBackupRoomFixture(t, dest, placeholder)
	engine.SetProtocols(&outboundProtocol{})
	witness := copyRestoreFixture(t, dest)
	witnessEngine := testRestoreEngine(t, witness)
	witnessEngine.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	pending := queueBackupFixture(t, engine, room.ID, "our archived body", nil)
	at := pending.CreatedAt.Add(time.Minute)
	if _, err := dest.TransitionMessage(ctx, pending.ID, "queued", "dispatching", migrationID(), at, at); err != nil {
		t.Fatal(err)
	}
	original, err := dest.TransitionMessage(ctx, pending.ID, "dispatching", "submitted", migrationID(), at, at)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "room:" + room.Address, Author: room.Address + "/Tester", ID: "already-retained-stanza"}
	echo := application.Incoming{
		AccountID: room.AccountID, Address: room.Address, Kind: "room", Historical: true, SenderName: "Tester",
		CreatedAt: at, OriginalText: pending.OriginalText, ProviderID: "room:archived-wire", ArchiveScope: room.Address,
		ArchiveID: "already-retained-archive-record", References: []domain.MessageReference{ref},
	}
	if err := engine.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	localKey := dbgen.GetArchiveAliasParams{AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: echo.ArchiveID}
	owner, err := dest.queries.GetArchiveAlias(ctx, localKey)
	if err != nil || owner == string(pending.ID) {
		t.Fatalf("local unattributed packet did not establish an independent archive identity: %s, %v", owner, err)
	}
	// Another copy received authenticated own attribution for a packet
	// whose archive alias or provider reference is already in use locally.
	echo.IsOwn, echo.SenderAddress, echo.CanonicalID = true, "alice@localhost", pending.ID
	independent := domain.MessageReference{Scope: ref.Scope, Author: ref.Author, ID: "uncontested-stanza-reference"}
	if sameArchiveAlias {
		echo.References = []domain.MessageReference{independent}
	} else {
		echo.ArchiveID = "another-archive-record"
	}
	if err := witnessEngine.Receive(ctx, echo); err != nil {
		t.Fatal(err)
	}
	const additionalArchiveID = "uncontested-archive-record"
	additional := echo
	additional.ArchiveID, additional.References = additionalArchiveID, []domain.MessageReference{independent}
	if err := witnessEngine.Receive(ctx, additional); err != nil {
		t.Fatal(err)
	}
	now := at.Add(48 * time.Hour)
	quote := "> " + pending.OriginalText + "\n"
	reply := replySeed(t, dest, room, quote+"a recent reply", now, nil, &domain.ReplyReference{
		Target: ref, Fallback: []domain.TextRange{{End: len(quote)}},
	})
	checkReply := func() {
		t.Helper()
		current, err := dest.Message(ctx, reply.ID)
		if err != nil || current.Reply == nil || !current.Reply.Available || current.Reply.MessageID != domain.ID(owner) {
			t.Fatalf("existing reply lost its retained target: %+v, %v", current.Reply, err)
		}
	}
	checkReply()
	days := 1
	if _, err := engine.SetConversationRetention(ctx, room.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, witness, false)
	for range 2 {
		if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
			t.Fatalf("older witness changed retained history: %+v, %v", result, err)
		}
		checkReply()
		stored, err := dest.Message(ctx, original.ID)
		if err != nil || stored.RevisionID != original.RevisionID || stored.OriginalText != original.OriginalText {
			t.Fatalf("older echo modified the own original: %+v, %v", stored, err)
		}
		if len(stored.References) != 1 || stored.References[0] != independent {
			t.Fatalf("restore should retain the independent reference only: %+v", stored.References)
		}
		if alias, err := dest.queries.GetArchiveAlias(ctx, localKey); err != nil || alias != owner {
			t.Fatalf("restore moved the existing archive alias: %s, %v", alias, err)
		}
		additionalKey := localKey
		additionalKey.ArchiveID = additionalArchiveID
		if alias, err := dest.queries.GetArchiveAlias(ctx, additionalKey); err != nil || alias != string(original.ID) {
			t.Fatalf("restore did not attach the uncontested archive alias: %s, %v", alias, err)
		}
		if !sameArchiveAlias {
			witnessKey := localKey
			witnessKey.ArchiveID = echo.ArchiveID
			if alias, err := dest.queries.GetArchiveAlias(ctx, witnessKey); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("restore attached the competing packet to the own original: %s, %v", alias, err)
			}
		}
	}
}
