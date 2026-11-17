package xmpp

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestBackupRestoreReconcilesRecordedNativeMentionWire(t *testing.T) {
	for _, state := range []string{"queued", "unknown", "submitted"} {
		t.Run(state, func(t *testing.T) {
			testBackupRestoreRecordedNativeMentionWire(t, state)
		})
	}
}

func testBackupRestoreRecordedNativeMentionWire(t *testing.T, localState string) {
	ctx := t.Context()
	newID := func() (domain.ID, error) {
		id, err := uuid.NewV7()
		return domain.ID(id.String()), err
	}
	mint := func() domain.ID {
		id, err := newID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	open := func(dir string) (*sqlite.Store, *application.Engine) {
		t.Helper()
		s, err := sqlite.Open(ctx, dir, mint())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		e, err := application.New(s, "local", newID, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		e.SetProtocols(echoMentionProtocol{Manager: New(dir, nil), uri: "xmpp:juliet@localhost"})
		return s, e
	}
	sourceDir, destinationDir := t.TempDir(), t.TempDir()
	source, sender := open(sourceDir)
	registered, err := sender.Register(ctx, mint(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	a, now := registered.Account, time.Now().UTC()
	room := domain.Conversation{ID: mint(), RevisionID: mint(), PeerID: mint(), AccountID: a.ID, Address: "room@conference.localhost", Title: "Room", Kind: "room", UpdatedAt: now}
	op := application.Operation{ID: mint(), ResourceID: room.ID, CreatedAt: now, Kind: "open_conversation"}
	if _, err := source.OpenConversation(ctx, application.ConversationMutation{Operation: op, Fingerprint: "room", Conversation: room}); err != nil {
		t.Fatal(err)
	}
	intent := domain.RoomSession{ConversationID: room.ID, AccountID: a.ID, IntentID: mint(), IntentRevisionID: mint(), Active: true, Nickname: "Alice", UpdatedAt: now}
	op.ID, op.Kind = mint(), "join_room"
	if _, err := source.SetRoomIntent(ctx, application.RoomIntentMutation{Operation: op, Fingerprint: "join", Session: intent}); err != nil {
		t.Fatal(err)
	}
	const body = "Juliet hello"
	accepted, err := sender.SendComposition(ctx, mint(), room.ID, domain.OutgoingComposition{OriginalText: body, Mentions: []domain.MentionSelection{{ParticipantID: "chosen-participant", Begin: 0, End: 6}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pending := accepted.Message
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destinationDir, "oie.sqlite"), data, 0600); err != nil {
		t.Fatal(err)
	}
	source, _ = open(sourceDir)
	destination, _ := open(destinationDir)
	wire, err := buildOutgoing(a, application.Outgoing{ID: pending.ID, Kind: "room", Address: room.Address, OriginalText: body, Mentions: pending.Mentions})
	if err != nil || len(wire.Mentions) != 1 {
		t.Fatalf("could not prepare the actual structured outgoing stanza: %+v, %v", wire.Mentions, err)
	}
	at := now.Add(time.Minute)
	// Both profiles retain the same composition. Only one attempt kept the
	// mention after checking the room policy at the wire boundary.
	var localRevision domain.ID
	for _, attempt := range []struct {
		store *sqlite.Store
		proof domain.WireMentionProof
		state string
	}{
		{destination, outgoingMentionProof(nil), localState},
		{source, outgoingMentionProof(wire.Mentions), "submitted"},
	} {
		claim, err := attempt.store.ClaimDispatchMessage(ctx, pending.ID, pending.RevisionID, intent.IntentRevisionID, mint(), at, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := attempt.store.RecordOriginalWireAttempt(ctx, pending.ID, claim.RevisionID, application.Outgoing{OriginalText: body}, "", attempt.proof); err != nil {
			t.Fatal(err)
		}
		finished, err := attempt.store.TransitionMessage(ctx, pending.ID, "dispatching", attempt.state, mint(), at, at)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.store == destination {
			localRevision = finished.RevisionID
		}
		if err := attempt.store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	backup := writeRoomEchoBackup(t, sourceDir)
	if _, err := sqlite.RestoreProfileBackup(ctx, destinationDir, backup, mint()); err != nil {
		t.Fatal(err)
	}
	if result, err := sqlite.RestoreProfileBackup(ctx, destinationDir, backup, mint()); err != nil || result != (sqlite.RestoreSummary{}) {
		t.Fatalf("repeating the terminal backup changed history: %+v, %v", result, err)
	}
	destination, destEngine := open(destinationDir)
	before, err := destination.Message(ctx, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedState, expectedWire, wantRows := localState, domain.WireMentionsAbsent, 2
	if localState == "queued" {
		expectedState, expectedWire, wantRows = "submitted", domain.WireMentionsPresent, 1
	}
	if before.SendState != expectedState || before.WireMentions != expectedWire ||
		(expectedWire == domain.WireMentionsPresent && before.WireMentionDigest != outgoingMentionProof(wire.Mentions).Digest) {
		t.Fatalf("wrong native wire after restore: state=%s, wire=%s, digest=%q", before.SendState, before.WireMentions, before.WireMentionDigest)
	}
	db, err := sql.Open("sqlite", filepath.Join(destinationDir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var oldWire string
	if err := db.QueryRowContext(ctx, "SELECT json_extract(payload,'$.WireMentions') FROM message_revisions WHERE entity_id=? AND id=?", pending.ID, localRevision).Scan(&oldWire); err != nil || oldWire != string(domain.WireMentionsAbsent) {
		t.Fatalf("the prior raw revision changed: %q, %v", oldWire, err)
	}
	refXML, err := xml.Marshal(wire.Mentions[0])
	if err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="archive">
  <forwarded xmlns="urn:xmpp:forward:0">
    <delay xmlns="urn:xmpp:delay" stamp="%s"/>
    <message xmlns="jabber:client" from="%s/Alice" id="%s" type="groupchat">
      <body>%s</body>
      <origin-id xmlns="urn:xmpp:sid:0" id="%s"/>
      <stanza-id xmlns="urn:xmpp:sid:0" by="%s" id="actual-structured-stanza"/>
      <x xmlns="http://jabber.org/protocol/muc#user"><item jid="alice@localhost/desktop"/></x>
      %s
    </message>
  </forwarded>
</result>`, at.Format(time.RFC3339Nano), room.Address, pending.ID, body, pending.ID, room.Address, refXML)
	var archived archiveResult
	if err := xml.Unmarshal([]byte(raw), &archived); err != nil {
		t.Fatal(err)
	}
	incoming, ok, err := archivedIncoming(a, room.Address, &archived, archiveTrust{SIDVerified: true})
	if err != nil || !ok || !incoming.IsOwn || incoming.WireMentionDigest != outgoingMentionProof(wire.Mentions).Digest {
		t.Fatalf("invalid actual echo: %+v %v %v", incoming, ok, err)
	}
	if err := destEngine.Receive(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	history, err := destination.Messages(ctx, room.ID, "", 10)
	if err != nil || len(history) != wantRows {
		for _, m := range history {
			t.Logf("row %s state=%s wire=%s digest=%s references=%v", m.ID, m.SendState, m.WireMentions, m.WireMentionDigest, m.References)
		}
		t.Fatalf("expected %d logical messages, got %d: %v", wantRows, len(history), err)
	}
	current, err := destination.Message(ctx, pending.ID)
	if err != nil || current.SendState != expectedState || current.WireMentions != expectedWire ||
		(wantRows == 1 && len(current.References) != 1) || (wantRows == 2 && len(current.References) != 0) {
		t.Fatalf("the archived echo was associated with the wrong wire attempt: %+v, %v", current, err)
	}
	if due, err := destination.PendingMessages(ctx, at.Add(time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("terminal evidence left an executable send: %+v, %v", due, err)
	}
}
