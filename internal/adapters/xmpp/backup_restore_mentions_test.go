package xmpp

import (
	"bytes"
	"context"
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

type roomEchoMentionCase struct {
	name, plannedURI, wireURI string
	wireEnd                   int
	live, tracked, resume     bool
	oldArchive, presenceOnly  bool
	recordedPlain             bool
	wantDistinct              bool
	wantState                 string
}

type echoMentionProtocol struct {
	*Manager
	uri string
}

func (p echoMentionProtocol) MentionCandidates(context.Context, domain.Account, domain.Conversation) (domain.MentionCandidates, error) {
	return domain.MentionCandidates{Items: []domain.MentionCandidate{{
		ID: "chosen-participant", InsertText: "@Juliet", ReferenceMode: "structured", URI: p.uri,
	}}}, nil
}

func TestOwnRoomEchoDistinguishesStructuredMentionsFromPlainText(t *testing.T) {
	const juliet = "xmpp:juliet@localhost"
	for _, tc := range []roomEchoMentionCase{
		{name: "archived structured mention", wireURI: juliet, wantDistinct: true, wantState: "unknown"},
		{name: "archived plain text", wantState: "submitted"},
		{name: "older room backup without wire evidence", oldArchive: true, wantState: "unknown"},
		{name: "live structured mention", wireURI: juliet, live: true, wantDistinct: true, wantState: "unknown"},
		{name: "live structured mention with an existing tracked plain attempt", wireURI: juliet, live: true, tracked: true, wantDistinct: true, wantState: "unknown"},
		{name: "live plain text", live: true, wantState: "submitted"},
		{name: "live different URI after explicit resume", plannedURI: juliet, wireURI: "xmpp:another-juliet@localhost", live: true, resume: true, wantDistinct: true, wantState: "unknown"},
		{name: "archived different URI", plannedURI: juliet, wireURI: "xmpp:another-juliet@localhost", wantDistinct: true, wantState: "unknown"},
		{name: "archived different span", plannedURI: juliet, wireURI: juliet, wireEnd: 5, wantDistinct: true, wantState: "unknown"},
		{name: "archived plain copy of a structured plan", plannedURI: juliet, wantDistinct: true, wantState: "unknown"},
		{name: "archived presence without exact evidence", plannedURI: juliet, wireURI: juliet, presenceOnly: true, wantDistinct: true, wantState: "unknown"},
		{name: "archived exact structured mention", plannedURI: juliet, wireURI: juliet, wantState: "unknown"},
		{name: "live exact structured mention", plannedURI: juliet, wireURI: juliet, live: true, tracked: true, wantState: "unknown"},
		{name: "live actual plain wire after structured selection", plannedURI: juliet, live: true, recordedPlain: true, wantState: "submitted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkOwnRoomEchoMentions(t, tc)
		})
	}
}

func checkOwnRoomEchoMentions(t *testing.T, tc roomEchoMentionCase) {
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
	if !tc.live || tc.resume {
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
	if tc.plannedURI != "" {
		engine.SetProtocols(echoMentionProtocol{Manager: New(destinationDir, nil), uri: tc.plannedURI})
		composition.Mentions = []domain.MentionSelection{{ParticipantID: "chosen-participant", Begin: 0, End: 6}}
	}
	pending, err := engine.SendComposition(ctx, key, room.ID, composition, nil)
	if err != nil {
		t.Fatal(err)
	}
	origin := pending.Message.ID
	if tc.resume {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if err := witness.Close(); err != nil {
			t.Fatal(err)
		}
		archivePath := writeRoomEchoBackup(t, destinationDir)
		database, err := os.ReadFile(filepath.Join(witnessDir, "oie.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destinationDir, "oie.sqlite"), database, 0600); err != nil {
			t.Fatal(err)
		}
		if result, err := sqlite.RestoreProfileBackup(ctx, destinationDir, archivePath, mint()); err != nil || result.PausedSends != 1 {
			t.Fatalf("did not restore a paused send before explicit review: %+v, %v", result, err)
		}
		store, engine = open(destinationDir)
		if err := store.ReviewRecoveredSend(ctx, origin, pending.Message.RevisionID, true, now); err != nil {
			t.Fatal(err)
		}
	}
	if tc.recordedPlain {
		claimed, err := store.ClaimDispatchMessage(ctx, origin, pending.Message.RevisionID, intent.IntentRevisionID, mint(), now.Add(time.Minute), true)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordOriginalWireAttempt(ctx, origin, claimed.RevisionID, application.Outgoing{OriginalText: body}, "", outgoingMentionProof(nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.TransitionMessage(ctx, origin, "dispatching", "submitted", mint(), now.Add(time.Minute), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, engine = open(destinationDir)
	}
	var wireMention []byte
	if tc.wireURI != "" {
		end := tc.wireEnd
		if end == 0 {
			end = 6
		}
		wire, err := buildOutgoing(account, application.Outgoing{
			ID: origin, Kind: "room", Address: room.Address, OriginalText: body,
			Mentions: []domain.MessageMention{
				{MentionSelection: domain.MentionSelection{ParticipantID: "opaque-participant", Begin: 0, End: end}, ReferenceMode: "structured", URI: tc.wireURI},
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
	if tc.live {
		manager := New(destinationDir, nil)
		manager.host = engine
		session := &mx.Session{}
		slot := &roomSlot{conversation: room, session: session, snapshot: roomSnapshot(room, "joined", intent.Nickname, "", intent.IntentRevisionID)}
		e := &entry{account: account, session: session, state: domain.Connection{AccountID: account.ID, State: "connected"},
			rooms: map[string]*roomSlot{room.Address: slot}, sidScopes: map[string]bool{room.Address: true}}
		if tc.tracked {
			tracked := ownRoomMessage{address: room.Address, body: body}
			if tc.plannedURI != "" {
				tracked.mentionDigest = domain.WireMentionDigest([]domain.WireMentionRange{{URI: tc.plannedURI, Begin: 0, End: 6}})
			}
			e.ownRoomMessages = map[string]ownRoomMessage{string(origin): tracked}
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
		if tc.oldArchive {
			in.WireMentions = ""
		}
		if tc.presenceOnly {
			in.WireMentionDigest = ""
		}
		if err := observer.Receive(ctx, in); err != nil {
			t.Fatal(err)
		}
		if err := witness.Close(); err != nil {
			t.Fatal(err)
		}
		archivePath := writeRoomEchoBackup(t, witnessDir)
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
	if err != nil || got.SendState != tc.wantState || got.OriginalText != body || !slices.Equal(got.Mentions, pending.Message.Mentions) {
		t.Fatalf("original composition after echo: state=%q, body=%q, mentions=%+v, error=%v", got.SendState, got.OriginalText, got.Mentions, err)
	}
	if tc.wantDistinct && len(got.References) != 0 {
		t.Fatalf("the structured stanza was linked to a distinct send: %+v", got.References)
	}
	if !tc.wantDistinct && tc.wireURI != "" && len(got.References) != 1 {
		t.Fatalf("the exact structured witness did not retain its reference: %+v", got.References)
	}
	if tc.recordedPlain && (got.WireMentions != domain.WireMentionsAbsent || len(got.References) != 1) {
		t.Fatalf("the exact plain wire proof did not survive reopening: %q, %+v", got.WireMentions, got.References)
	}
	history, err := store.Messages(ctx, room.ID, "", 10)
	wantRows := 1
	if tc.wantDistinct {
		wantRows = 2
	}
	if err != nil || len(history) != wantRows {
		t.Fatalf("expected %d history rows after echo: %+v, %v", wantRows, history, err)
	}
	observed := domain.WireMentionsAbsent
	if tc.wireURI != "" {
		observed = domain.WireMentionsPresent
	}
	if tc.wantDistinct && !slices.ContainsFunc(history, func(message domain.Message) bool {
		return message.ID != origin && message.WireMentions == observed && len(message.Mentions) == 0
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

func writeRoomEchoBackup(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.zip")
	var archive bytes.Buffer
	if err := sqlite.WriteProfileBackup(t.Context(), dir, &archive, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
