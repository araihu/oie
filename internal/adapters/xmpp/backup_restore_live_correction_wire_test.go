package xmpp

import (
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRejectsDifferentLiveCorrectionWire(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	dir := t.TempDir()
	store, engine, manager := openDirectEchoProfile(t, dir)
	engine.SetProtocols(archiveEditFixtureProtocol{manager})
	account, chat := setupDirectEchoProfile(t, engine)
	// Typed reply points at a real retained message; the native correction below is plain.
	quoted := domain.MessageReference{ID: "earlier-message", Scope: "sender:" + account.Address, Author: account.Address}
	if err := engine.Receive(ctx, application.Incoming{AccountID: account.ID, Address: chat.Address, Kind: "direct", IsOwn: true, Historical: true,
		ProviderID: quoted.ID, References: []domain.MessageReference{quoted}, OriginalText: "unrelated earlier message", CreatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	root, err := engine.SendMessage(ctx, mintDirectEchoID(t), chat.ID, "own originally plain message")
	if err != nil {
		t.Fatal(err)
	}
	for _, states := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
		if _, err := store.TransitionMessage(ctx, root.Message.ID, states[0], states[1], mintDirectEchoID(t), now, now); err != nil {
			t.Fatal(err)
		}
	}
	target, err := engine.MessageEditTarget(ctx, root.Message.ID)
	if err != nil || !target.Eligible {
		t.Fatalf("fixture root not editable: %+v %v", target, err)
	}
	key := mintDirectEchoID(t)
	const body = "plain corrected text"
	if edit, err := engine.EditMessage(ctx, key, root.Message.ID, target.CurrentRevisionID, body); err != nil || edit.Command.State != "queued" {
		t.Fatalf("accept: %+v %v", edit, err)
	}
	if _, err := store.ClaimMessageEdit(ctx, key, mintDirectEchoID(t), now); err != nil {
		t.Fatal(err)
	}
	terminal, err := store.FinishMessageEdit(ctx, key, "submitted", mintDirectEchoID(t), now, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	archive := writeRoomEchoBackup(t, dir)
	recoveredDir := t.TempDir()
	if _, err := sqlite.RestoreProfileBackup(ctx, recoveredDir, archive, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	recovered, receiver, receiverManager := openDirectEchoProfile(t, recoveredDir)
	original, err := recovered.Message(ctx, root.Message.ID)
	if err != nil || !original.Edited || original.Reply != nil || domain.DisplayBody(original) != body {
		t.Fatalf("restore did not produce a plain corrected message: %+v %v", original, err)
	}

	conflictingReply := fmt.Sprintf(`<reply xmlns="urn:xmpp:reply:0" id="%s" to="%s"/><fallback xmlns="urn:xmpp:fallback:0" for="urn:xmpp:reply:0"><body start="0" end="6"/></fallback>`, quoted.ID, account.Address)
	packet := func(metadata string) string {
		return fmt.Sprintf(`<message xmlns="jabber:client" from="%s/other-client" to="%s" type="chat" id="%s"><body>%s</body><origin-id xmlns="urn:xmpp:sid:0" id="%s"/><replace xmlns="urn:xmpp:message-correct:0" id="%s"/>%s</message>`,
			account.Address, chat.Address, key, body, key, root.Message.ID, metadata)
	}
	observe := func(archiveKey, metadata string) {
		t.Helper()
		raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/>%s</forwarded></result>`,
			archiveKey, now.Format(time.RFC3339Nano), packet(metadata))
		var result archiveResult
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		incoming, ok, err := archivedIncoming(account, account.Address, &result, archiveTrust{})
		if err != nil || !ok || !incoming.IsOwn || (incoming.Reply == nil) != (metadata == "") ||
			incoming.CanonicalID != key || incoming.Correction == nil || incoming.Correction.TargetID != string(root.Message.ID) {
			t.Fatalf("bad archive fixture: %+v, %v, %v", incoming, ok, err)
		}
		if err := receiver.Receive(ctx, incoming); err != nil {
			t.Fatal(err)
		}
	}
	check := func() {
		t.Helper()
		after, err := recovered.Message(ctx, root.Message.ID)
		if err != nil || !after.Edited || after.RevisionID != original.RevisionID || after.ProjectionSourceID != original.ProjectionSourceID ||
			after.Reply != nil || domain.DisplayBody(after) != body {
			t.Fatalf("an incompatible replay changed the native plain correction: %+v, %v", after, err)
		}
		replay, err := receiver.EditMessage(ctx, key, root.Message.ID, target.CurrentRevisionID, body)
		if err != nil || !replay.Replayed || replay.Command.State != "submitted" || replay.Command.RevisionID != terminal.RevisionID {
			t.Fatalf("correction replay changed its accepted outcome: %+v, %v", replay, err)
		}
	}

	const mismatchedKey = "mismatched-reply-shape"
	observe(mismatchedKey, conflictingReply)
	check()
	db, err := sql.Open("sqlite", filepath.Join(recoveredDir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?)`,
		account.ID, account.Address, mismatchedKey).Scan(&exists); err != nil || exists {
		t.Fatalf("the incompatible correction acquired an alias: %v, %v", exists, err)
	}
	const exactKey = "exact-correction-shape"
	observe(exactKey, "")
	assertDirectEchoArchiveOwner(t, recoveredDir, account, exactKey, original.ProjectionSourceID)
	observe(exactKey, conflictingReply)
	assertDirectEchoArchiveOwner(t, recoveredDir, account, exactKey, original.ProjectionSourceID)
	receiveLiveDirectEchoPacket(t, receiverManager, account, packet(conflictingReply))
	check()
}

type restoredRoomEditFixtureProtocol struct {
	*Manager
	intent        domain.ID
	scope, author string
}

func (p *restoredRoomEditFixtureProtocol) RoomSession(_ context.Context, account domain.Account, room domain.Conversation) (domain.RoomSession, error) {
	return domain.RoomSession{AccountID: account.ID, ConversationID: room.ID, Active: true, State: "joined", IntentRevisionID: p.intent}, nil
}

func (p *restoredRoomEditFixtureProtocol) PrepareCorrection(_ context.Context, _ domain.Account, _ domain.Conversation, message domain.Message) (domain.OutgoingCorrection, error) {
	return domain.OutgoingCorrection{
		TargetMessageID: message.ID, OriginalWireID: message.ProviderID, Author: p.author, Scope: p.scope,
		SessionID: "fixture-room-edit", RoomIntentRevisionID: p.intent,
	}, nil
}

func TestBackupRestoreMatchesRoomCorrectionReplayWithOccupantProvider(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	dir := t.TempDir()
	store, engine, manager := openDirectEchoProfile(t, dir)
	protocol := &restoredRoomEditFixtureProtocol{Manager: manager}
	engine.SetProtocols(protocol)
	account, _ := setupDirectEchoProfile(t, engine)
	const occupantID = "verified-room-occupant"
	opened, err := engine.OpenRoom(ctx, mintDirectEchoID(t), account.ID, "room@conference.localhost", "Alice", "", false)
	if err != nil {
		t.Fatal(err)
	}
	room := opened.Conversation
	intent, err := store.RoomIntent(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	protocol.intent, protocol.author = intent.IntentRevisionID, room.Address+"/Alice"
	protocol.scope = roomCorrectionScope(room.Address, protocol.author, occupantID)
	root, err := engine.SendMessage(ctx, mintDirectEchoID(t), room.ID, "native room original")
	if err != nil {
		t.Fatal(err)
	}
	for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
		if _, err := store.TransitionMessage(ctx, root.Message.ID, transition[0], transition[1], mintDirectEchoID(t), now, now); err != nil {
			t.Fatal(err)
		}
	}
	target, err := engine.MessageEditTarget(ctx, root.Message.ID)
	if err != nil || !target.Eligible {
		t.Fatalf("room edit fixture: %+v, %v", target, err)
	}
	key := mintDirectEchoID(t)
	const body = "Alice preserves the plain correction"
	if _, err := engine.EditMessage(ctx, key, root.Message.ID, target.CurrentRevisionID, body); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimMessageEdit(ctx, key, mintDirectEchoID(t), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishMessageEdit(ctx, key, "submitted", mintDirectEchoID(t), now, now, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	archive := writeRoomEchoBackup(t, dir)
	destination := t.TempDir()
	if _, err := sqlite.RestoreProfileBackup(ctx, destination, archive, mintDirectEchoID(t)); err != nil {
		t.Fatal(err)
	}
	recovered, receiver, _ := openDirectEchoProfile(t, destination)
	observe := func(keyID, metadata string) {
		t.Helper()
		// This archive carries the ordinary correction ID and the trusted
		// occupant, with no separate XEP-0359 origin ID.
		raw := fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s" type="groupchat" id="%s"><body>%s</body><replace xmlns="urn:xmpp:message-correct:0" id="%s"/><occupant-id xmlns="urn:xmpp:occupant-id:0" id="%s"/><x xmlns="http://jabber.org/protocol/muc#user"><item jid="%s/other-client"/></x>%s</message></forwarded></result>`,
			keyID, now.Format(time.RFC3339Nano), protocol.author, key, body, root.Message.ID, occupantID, account.Address, metadata)
		var result archiveResult
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		in, ok, err := archivedIncoming(account, room.Address, &result, archiveTrust{SIDVerified: true, OccupantVerified: true})
		if err != nil || !ok || !in.IsOwn || in.CanonicalID != "" || in.ProviderID == string(key) || in.Correction == nil ||
			in.Correction.ID != string(key) || in.Correction.Scope != protocol.scope {
			t.Fatalf("wrong room archive fixture: %+v, %v, %v", in, ok, err)
		}
		if err := receiver.Receive(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	mention := `<reference xmlns="urn:xmpp:reference:0" type="mention" uri="xmpp:alice@localhost" begin="0" end="5"/>`
	observe("room-mismatched-mention", mention)
	observe("room-matching-correction", "")
	messages, err := recovered.Messages(ctx, room.ID, "", 10)
	if err != nil || len(messages) != 1 || messages[0].ID != root.Message.ID || !messages[0].Edited ||
		messages[0].ProjectionSourceID != key || domain.DisplayBody(messages[0]) != body {
		t.Fatalf("room archive changed the retained correction or exposed a duplicate: %+v, %v", messages, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(destination, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	var owner domain.ID
	if err := db.QueryRowContext(ctx, `SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?`,
		account.ID, room.Address, "room-matching-correction").Scan(&owner); err != nil || owner != key {
		t.Fatalf("verified room archive key did not bind to the native source: %s, %v", owner, err)
	}
}
