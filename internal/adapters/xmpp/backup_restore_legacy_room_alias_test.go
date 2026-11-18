package xmpp

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func TestBackupRestoreKeepsDistinctStructuredPacketBehindLegacyRoomAlias(t *testing.T) {
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
		e.SetProtocols(New(dir, nil))
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
	pending, err := sender.SendMessage(ctx, mint(), room.ID, body)
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Minute)
	if _, err := source.TransitionMessage(ctx, pending.Message.ID, "queued", "dispatching", mint(), at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, pending.Message.ID, "dispatching", "submitted", mint(), at, at); err != nil {
		t.Fatal(err)
	}
	wire, err := buildOutgoing(a, application.Outgoing{
		ID: pending.Message.ID, Kind: "room", Address: room.Address, OriginalText: body,
		Mentions: []domain.MessageMention{{MentionSelection: domain.MentionSelection{ParticipantID: "remote", Begin: 0, End: 6}, ReferenceMode: "structured", URI: "xmpp:juliet@localhost"}},
	})
	if err != nil || len(wire.Mentions) != 1 {
		t.Fatalf("cannot create a true structured packet: %v %v", wire.Mentions, err)
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
</result>`, at.Format(time.RFC3339Nano), room.Address, pending.Message.ID, body, pending.Message.ID, room.Address, refXML)
	var archived archiveResult
	if err := xml.Unmarshal([]byte(raw), &archived); err != nil {
		t.Fatal(err)
	}
	incoming, ok, err := archivedIncoming(a, room.Address, &archived, archiveTrust{SIDVerified: true})
	if err != nil || !ok || !incoming.IsOwn || len(incoming.References) != 1 || incoming.WireMentionDigest != outgoingMentionProof(wire.Mentions).Digest {
		t.Fatalf("invalid actual echo: %+v %v %v", incoming, ok, err)
	}
	// Older room archives could bind this packet to the native row using
	// the same origin and text alone.
	legacy := incoming
	legacy.WireMentions, legacy.WireMentionDigest = "", ""
	if err := sender.Receive(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if history, err := source.Messages(ctx, room.ID, "", 10); err != nil || len(history) != 1 || history[0].ID != pending.Message.ID {
		t.Fatalf("fixture did not bind legacy archive key to native send: %+v %v", history, err)
	}
	if err := sender.Receive(ctx, application.Incoming{
		AccountID: a.ID, Address: room.Address, Kind: "room", Historical: true,
		SenderName: "Juliet", ProviderID: "room:answer", OriginalText: "an existing reply", CreatedAt: at.Add(time.Second),
		Reply: &domain.ReplyReference{Target: incoming.References[0]},
	}); err != nil {
		t.Fatal(err)
	}
	history, err := source.Messages(ctx, room.ID, "", 10)
	if err != nil || len(history) != 2 || history[0].Reply == nil || history[0].Reply.MessageID != pending.Message.ID {
		t.Fatalf("fixture reply did not resolve through the legacy reference: %+v, %v", history, err)
	}
	replyID := history[0].ID
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	backup := writeRoomEchoBackup(t, sourceDir)
	if _, err := sqlite.RestoreProfileBackup(ctx, destinationDir, backup, mint()); err != nil {
		t.Fatal(err)
	}
	destination, observer := open(destinationDir)
	var witnessID domain.ID
	for range 2 {
		if err := observer.Receive(ctx, incoming); err != nil {
			t.Fatal(err)
		}
		history, err := destination.Messages(ctx, room.ID, "", 10)
		if err != nil || len(history) != 3 {
			t.Fatalf("legacy room archive key swallowed the different actual wire composition: %d rows, error %v", len(history), err)
		}
		for _, row := range history {
			if row.ID == replyID {
				if row.Reply == nil || !row.Reply.Available || row.Reply.MessageID != pending.Message.ID {
					t.Fatalf("archive recovery disturbed an existing reply owner: %+v", row.Reply)
				}
			} else if row.ID != pending.Message.ID {
				if !row.IsOwn || row.WireMentionDigest != incoming.WireMentionDigest || row.ProviderID != incoming.ProviderID ||
					(witnessID != "" && witnessID != row.ID) {
					t.Fatalf("actual own archived packet lost proof or stable identity: %+v", row)
				}
				witnessID = row.ID
			}
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(destinationDir, "oie.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var owner string
	if err := db.QueryRowContext(ctx, `SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?`,
		a.ID, incoming.ArchiveScope, incoming.ArchiveID).Scan(&owner); err != nil || owner != string(pending.Message.ID) {
		t.Fatalf("archive recovery moved the existing archive key to %q: %v", owner, err)
	}
}
