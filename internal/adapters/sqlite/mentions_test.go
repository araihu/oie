package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type mentionProtocol struct {
	*outboundProtocol
	scope      domain.ID
	candidates []domain.MentionCandidate
	reads      int
}

func (p *mentionProtocol) MentionCandidates(_ context.Context, a domain.Account, c domain.Conversation) (domain.MentionCandidates, error) {
	p.reads++
	out := domain.MentionCandidates{ConversationID: c.ID, AccountID: a.ID, State: "known", Items: []domain.MentionCandidate{}}
	if c.ID == p.scope {
		out.Items = append(out.Items, p.candidates...)
	}
	return out, nil
}
func TestMentionsFreezeUnicodeTargetsThroughOutboxRestartAndReplay(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	makeEngine := func(store *Store, p *mentionProtocol) *application.Engine {
		e, err := application.New(store, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		e.SetProtocols(p)
		return e
	}
	p := &mentionProtocol{outboundProtocol: &outboundProtocol{wireReply: true}}
	e := makeEngine(s, p)
	a, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	room := outboundRoom(t, s, a.Account.ID, "room@rooms.localhost")
	p.scope = room.ID
	p.candidates = []domain.MentionCandidate{{ID: "opaque-juliet", InsertText: "@Juliet", DisplayName: "Juliet", ReferenceMode: "structured", URI: "xmpp:juliet@localhost"}}
	source := replySeed(t, s, room, "Original 🍰", time.Now().UTC(), []domain.MessageReference{{Scope: "room:" + room.Address, ID: "original", Author: room.Address + "/Romeo"}}, nil)
	if _, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "Alice", ""); err != nil {
		t.Fatal(err)
	}
	text := "😀 @Juliet"
	selections := []domain.MentionSelection{{ParticipantID: "opaque-juliet", Begin: 2, End: 9}}
	key := migrationID()
	sent, err := e.SendMessageWithMentions(t.Context(), key, room.ID, text, source.ID, selections)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Message.Mentions) != 1 || sent.Message.Mentions[0].URI != "xmpp:juliet@localhost" || sent.Message.Mentions[0].Begin != 2 {
		t.Fatal(sent.Message)
	}
	// Accepted sends replay before participant refresh, including renamed/left users.
	p.candidates = nil
	reads := p.reads
	replay, err := e.SendMessageWithMentions(t.Context(), key, room.ID, text, source.ID, selections)
	if err != nil || !replay.Replayed || p.reads != reads || !reflect.DeepEqual(replay.Message.Mentions, sent.Message.Mentions) {
		t.Fatal("replay depended on mutable roster", replay, err, p.reads, reads)
	}
	changed := []domain.MentionSelection{{ParticipantID: "different-user", Begin: 2, End: 9}}
	if _, err := e.SendMessageWithMentions(t.Context(), key, room.ID, text, source.ID, changed); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("changed mention reused acceptance", err)
	}
	if _, err := e.SendMessageWithMentions(t.Context(), migrationID(), room.ID, text, source.ID, selections); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("stale candidate accepted", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	p2 := &mentionProtocol{outboundProtocol: &outboundProtocol{connected: true, autoJoined: true, wireReply: true}}
	e2 := makeEngine(s, p2)
	stored, err := e2.Message(t.Context(), sent.Message.ID)
	if err != nil || !reflect.DeepEqual(stored.Mentions, sent.Message.Mentions) {
		t.Fatal("restart lost frozen target", stored, err)
	}
	stop := startOutboundWorker(t, e2)
	defer stop()
	awaitOutbound(t, func() bool { p2.mu.Lock(); defer p2.mu.Unlock(); return len(p2.sent) == 1 })
	p2.mu.Lock()
	wire := p2.sent[0]
	p2.mu.Unlock()
	offset := utf8.RuneCountInString(stored.OriginalText) - utf8.RuneCountInString(text)
	if len(wire.Mentions) != 1 || wire.Mentions[0].Begin != 2+offset || wire.Mentions[0].URI != "xmpp:juliet@localhost" || p2.reads != 0 {
		t.Fatal("dispatch did not use frozen target with quote offset", wire, p2.reads)
	}
}
