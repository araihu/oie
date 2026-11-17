package xmpp

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
	mx "mellium.im/xmpp"
)

func TestOwnRoomEchoDistinguishesStructuredMentionsFromPlainText(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mentions   bool
		live       bool
		tracked    bool
		oldArchive bool
		wantState  string
	}{
		{name: "archived structured mention", mentions: true, wantState: "unknown"},
		{name: "archived plain text", wantState: "submitted"},
		{name: "older room backup without wire evidence", oldArchive: true, wantState: "unknown"},
		{name: "live structured mention", mentions: true, live: true, wantState: "unknown"},
		{name: "live structured mention with an existing tracked plain attempt", mentions: true, live: true, tracked: true, wantState: "unknown"},
		{name: "live plain text", live: true, wantState: "submitted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkOwnRoomEchoMentions(t, tc.mentions, tc.live, tc.tracked, tc.oldArchive, tc.wantState)
		})
	}
}

func checkOwnRoomEchoMentions(t *testing.T, mentions, live, tracked, oldArchive bool, wantState string) {
	ctx := t.Context()
	newID := func() (domain.ID, error) {
		id, err := uuid.NewV7()
		return domain.ID(id.String()), err
	}
	mint := func() domain.ID {
		t.Helper()
		id, err := newID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	open := func(dir string) (*sqlite.Store, *application.Engine) {
		t.Helper()
		store, err := sqlite.Open(ctx, dir, mint())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		engine, err := application.New(store, "local", newID, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		engine.SetProtocols(New(dir, nil))
		return store, engine
	}

	destinationDir := t.TempDir()
	store, engine := open(destinationDir)
	registered, err := engine.Register(ctx, mint(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	account, now := registered.Account, time.Now().UTC()
	room := domain.Conversation{ID: mint(), RevisionID: mint(), PeerID: mint(), AccountID: account.ID, Address: "room@conference.localhost", Title: "Room", Kind: "room", UpdatedAt: now}
	op := application.Operation{ID: mint(), ResourceID: room.ID, CreatedAt: now, Kind: "open_conversation"}
	if _, err := store.OpenConversation(ctx, application.ConversationMutation{Operation: op, Fingerprint: "room", Conversation: room}); err != nil {
		t.Fatal(err)
	}
	intent := domain.RoomSession{ConversationID: room.ID, AccountID: account.ID, IntentID: mint(), IntentRevisionID: mint(), Active: true, Nickname: "Alice", UpdatedAt: now}
	op.ID, op.Kind = mint(), "join_room"
	if _, err := store.SetRoomIntent(ctx, application.RoomIntentMutation{Operation: op, Fingerprint: "join", Session: intent}); err != nil {
		t.Fatal(err)
	}

	var witnessDir string
	var witness *sqlite.Store
	var observer *application.Engine
	if !live {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		witnessDir = t.TempDir()
		database, err := os.ReadFile(filepath.Join(destinationDir, "oie.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(witnessDir, "oie.sqlite"), database, 0600); err != nil {
			t.Fatal(err)
		}
		store, engine = open(destinationDir)
		witness, observer = open(witnessDir)
	}

	const body = "Juliet hello"
	key := mint()
	composition := domain.OutgoingComposition{OriginalText: body}
	pending, err := engine.SendComposition(ctx, key, room.ID, composition, nil)
	if err != nil {
		t.Fatal(err)
	}
	origin := pending.Message.ID
	var wireMention []byte
	if mentions {
		wire, err := buildOutgoing(account, application.Outgoing{
			ID: origin, Kind: "room", Address: room.Address, OriginalText: body,
			Mentions: []domain.MessageMention{
				{MentionSelection: domain.MentionSelection{ParticipantID: "opaque-participant", Begin: 0, End: 6}, ReferenceMode: "structured", URI: "xmpp:juliet@localhost"},
			},
		})
		if err != nil || len(wire.Mentions) != 1 {
			t.Fatalf("fixture did not encode a structured mention: %+v, %v", wire.Mentions, err)
		}
		wireMention, err = xml.Marshal(wire.Mentions[0])
		if err != nil {
			t.Fatal(err)
		}
	}
	raw := fmt.Sprintf(`<message xmlns="jabber:client" from="%s/Alice" id="%s" type="groupchat"><body>%s</body><origin-id xmlns="urn:xmpp:sid:0" id="%s"/><stanza-id xmlns="urn:xmpp:sid:0" by="%s" id="witnessed-stanza"/><x xmlns="http://jabber.org/protocol/muc#user"><item jid="alice@localhost/desktop"/></x>%s</message>`,
		room.Address, origin, body, origin, room.Address, wireMention)
	if live {
		manager := New(destinationDir, nil)
		manager.host = engine
		session := &mx.Session{}
		slot := &roomSlot{conversation: room, session: session, snapshot: roomSnapshot(room, "joined", intent.Nickname, "", intent.IntentRevisionID)}
		e := &entry{account: account, session: session, state: domain.Connection{AccountID: account.ID, State: "connected"},
			rooms: map[string]*roomSlot{room.Address: slot}, sidScopes: map[string]bool{room.Address: true}}
		if tracked {
			e.ownRoomMessages = map[string]ownRoomMessage{string(origin): {address: room.Address, body: body}}
		}
		decoder := xml.NewDecoder(strings.NewReader(raw))
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		start := token.(xml.StartElement)
		if err := manager.handle(ctx, e, tokens{decoder, xml.NewEncoder(&bytes.Buffer{})}, &start); err != nil {
			t.Fatal(err)
		}
	} else {
		raw = fmt.Sprintf(`<result xmlns="urn:xmpp:mam:2" id="archive"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/>%s</forwarded></result>`,
			now.Add(time.Minute).Format(time.RFC3339Nano), raw)
		var archived archiveResult
		if err := xml.Unmarshal([]byte(raw), &archived); err != nil {
			t.Fatal(err)
		}
		in, ok, err := archivedIncoming(account, room.Address, &archived, archiveTrust{SIDVerified: true})
		if err != nil || !ok || !in.IsOwn || in.CanonicalID != origin {
			t.Fatalf("fixture did not decode an authenticated own echo: %+v, %v, %v", in, ok, err)
		}
		if oldArchive {
			in.WireMentions = ""
		}
		if err := observer.Receive(ctx, in); err != nil {
			t.Fatal(err)
		}
		if err := witness.Close(); err != nil {
			t.Fatal(err)
		}
		archivePath := filepath.Join(t.TempDir(), "backup.zip")
		var archive bytes.Buffer
		if err := sqlite.WriteProfileBackup(ctx, witnessDir, &archive, false); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archivePath, archive.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlite.RestoreProfileBackup(ctx, destinationDir, archivePath, mint()); err != nil {
			t.Fatal(err)
		}
		if summary, err := sqlite.RestoreProfileBackup(ctx, destinationDir, archivePath, mint()); err != nil || summary != (sqlite.RestoreSummary{}) {
			t.Fatalf("repeated echo backup changed the result: %+v, %v", summary, err)
		}
		store, engine = open(destinationDir)
	}
	got, err := store.Message(ctx, origin)
	if err != nil || got.SendState != wantState || got.OriginalText != body || len(got.Mentions) != 0 {
		t.Fatalf("plain pending send after echo: state=%q, body=%q, mentions=%+v, error=%v", got.SendState, got.OriginalText, got.Mentions, err)
	}
	history, err := store.Messages(ctx, room.ID, "", 10)
	wantRows := 1
	if mentions {
		wantRows = 2
	}
	if err != nil || len(history) != wantRows {
		t.Fatalf("expected %d history rows after echo: %+v, %v", wantRows, history, err)
	}
	if mentions && len(got.References) != 0 {
		t.Fatalf("the structured stanza was linked to the distinct plain send: %+v", got.References)
	}
	if mentions && !slices.ContainsFunc(history, func(message domain.Message) bool {
		return message.ID != origin && message.WireMentions == domain.WireMentionsPresent && len(message.Mentions) == 0
	}) {
		t.Fatal("structured wire evidence or its separate message was lost")
	}
	if replay, err := engine.SendComposition(ctx, key, room.ID, composition, nil); err != nil || !replay.Replayed || replay.Message.ID != origin {
		t.Fatalf("original acceptance was lost: %+v, %v", replay, err)
	}
	if due, err := store.PendingMessages(ctx, now.Add(time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("received evidence left an executable copy: %+v, %v", due, err)
	}
}
