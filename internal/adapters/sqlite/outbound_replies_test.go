package sqlite

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

type outboundProtocol struct {
	selectHook                       func()
	mu                               sync.Mutex
	connected, wireReply, autoJoined bool
	sessions                         map[domain.ID]domain.RoomSession
	joins, leaves                    int
	sent                             []application.Outgoing
}

func (p *outboundProtocol) NormalizeAddress(_ string, address string) (string, error) {
	return address, nil
}
func (p *outboundProtocol) Configure(_ context.Context, a domain.Account, _ application.ConnectionInput) (domain.Connection, error) {
	return domain.Connection{AccountID: a.ID, State: "connected"}, nil
}
func (p *outboundProtocol) Disconnect(_ context.Context, a domain.Account) (domain.Connection, error) {
	return domain.Connection{AccountID: a.ID, State: "not_connected"}, nil
}
func (p *outboundProtocol) Connection(_ context.Context, a domain.Account) (domain.Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := "not_connected"
	if p.connected {
		state = "connected"
	}
	return domain.Connection{AccountID: a.ID, State: state}, nil
}
func (p *outboundProtocol) Send(_ context.Context, _ domain.Account, out application.Outgoing) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if out.Kind == "room" {
		found := false
		for _, s := range p.sessions {
			if s.State == "joined" && s.IntentRevisionID == out.RoomIntentRevisionID {
				found = true
			}
		}
		if !found {
			return application.NotSent
		}
	}
	p.sent = append(p.sent, out)
	return application.Submitted
}
func (p *outboundProtocol) OutgoingReferences(a domain.Account, out application.Outgoing) []domain.MessageReference {
	if out.Kind == "room" {
		return nil
	}
	return []domain.MessageReference{{Scope: "sender:" + a.Address, ID: string(out.ID), Author: a.Address}}
}
func (p *outboundProtocol) SelectReplyReference(_ domain.Account, _ domain.Conversation, m domain.Message) (domain.MessageReference, bool) {
	if p.selectHook != nil {
		p.selectHook()
	}
	if p.wireReply && len(m.References) > 0 {
		return m.References[0], true
	}
	return domain.MessageReference{}, false
}
func (p *outboundProtocol) RoomSession(_ context.Context, a domain.Account, c domain.Conversation) (domain.RoomSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[c.ID]; ok {
		return s, nil
	}
	return domain.RoomSession{AccountID: a.ID, ConversationID: c.ID, State: "not_joined", UpdatedAt: time.Now().UTC()}, nil
}
func (p *outboundProtocol) JoinRoom(_ context.Context, a domain.Account, c domain.Conversation, nickname string, revision domain.ID) (domain.RoomSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessions == nil {
		p.sessions = map[domain.ID]domain.RoomSession{}
	}
	if nickname == "" {
		nickname = "default nickname"
	}
	state := "joining"
	if p.autoJoined {
		state = "joined"
	}
	s := domain.RoomSession{AccountID: a.ID, ConversationID: c.ID, IntentRevisionID: revision, State: state, Nickname: nickname, UpdatedAt: time.Now().UTC()}
	p.sessions[c.ID] = s
	p.joins++
	return s, nil
}
func (p *outboundProtocol) LeaveRoom(_ context.Context, a domain.Account, c domain.Conversation, revision domain.ID) (domain.RoomSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := domain.RoomSession{AccountID: a.ID, ConversationID: c.ID, IntentRevisionID: revision, State: "not_joined", UpdatedAt: time.Now().UTC()}
	p.sessions[c.ID] = s
	p.leaves++
	return s, nil
}
func outboundRoom(t *testing.T, s *Store, account domain.ID, address string) domain.Conversation {
	t.Helper()
	c := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: account, PeerID: migrationID(), Kind: "room", Address: address, Title: address, UpdatedAt: time.Now().UTC()}
	got, err := s.OpenConversation(t.Context(), application.ConversationMutation{Operation: application.Operation{ID: migrationID(), ResourceID: c.ID, Kind: "open_conversation", CreatedAt: c.UpdatedAt}, Fingerprint: "room fixture", Conversation: c})
	if err != nil {
		t.Fatal(err)
	}
	return got.Conversation
}
func startOutboundWorker(t *testing.T, e *application.Engine) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
func awaitOutbound(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("outbound state timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOutboundReplyFreezesUnicodeQuoteReferenceAndReceipt(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &outboundProtocol{wireReply: true}
	e.SetProtocols(p)
	ref := domain.MessageReference{Scope: "sender:bob@localhost", ID: "original-ID", Author: "bob@localhost"}
	source := replySeed(t, s, c, "Olá 🍰\nSegunda linha é", time.Now().UTC(), []domain.MessageReference{ref}, nil)
	text := "Minha resposta 😀\ncom é"
	key := migrationID()
	sent, err := e.SendMessageWithReply(t.Context(), key, c.ID, text, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan := sent.Message.OutboundReply
	if plan == nil || plan.Reference == nil || *plan.Reference != ref || plan.TargetID != source.ID || sent.Message.Reply == nil || !sent.Message.Reply.Available || sent.Message.Reply.BodyText != text {
		t.Fatal("reply plan/projection missing", sent)
	}
	body, _ := domain.ReplyBody(sent.Message.OriginalText, plan.Fallback)
	if body != text || plan.Fallback[0].End == len(sent.Message.OriginalText)-len(text) || plan.Fallback[0].End != utf8.RuneCountInString(strings.TrimSuffix(sent.Message.OriginalText, text)) {
		t.Fatal("fallback is not exact Unicode code points", plan.Fallback)
	}
	current, err := s.Message(t.Context(), sent.Message.ID)
	if err != nil || !reflect.DeepEqual(current.OutboundReply, plan) || current.Reply.BodyText != text {
		t.Fatal("durable plan lost", err)
	}
	source.OriginalText = "edited source"
	source.RevisionID = migrationID()
	if err = s.queries.UpdateMessage(t.Context(), dbgen.UpdateMessageParams{RevisionID: string(source.RevisionID), State: source.SendState, Payload: jsonText(source), ID: string(source.ID)}); err != nil {
		t.Fatal(err)
	}
	replay, err := e.SendMessageWithReply(t.Context(), key, c.ID, text, source.ID)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Message.OutboundReply, plan) || replay.Message.Reply.BodyText != text {
		t.Fatal("receipt reselected edited source", err)
	}
	if _, err = e.SendMessageWithReply(t.Context(), key, c.ID, text, migrationID()); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("changed reply target replayed", err)
	}
	next, err := e.SendMessageWithReply(t.Context(), migrationID(), c.ID, text, source.ID)
	if err != nil || next.Message.OutboundReply.Reference != nil || next.Message.OutboundReply.Excerpt != "edited source" {
		t.Fatal("stale source-hash reference selected", err)
	}
}

func TestReplyScopeAndAcceptanceRollback(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{wireReply: true})
	other := outboundRoom(t, s, c.AccountID, "other@rooms.localhost")
	foreign := replySeed(t, s, other, "foreign", time.Now().UTC(), nil, nil)
	if _, err := e.SendMessageWithReply(t.Context(), migrationID(), c.ID, "answer", foreign.ID); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("cross-conversation target accepted", err)
	}
	source := replySeed(t, s, c, "retained", time.Now().UTC(), []domain.MessageReference{{Scope: "sender:bob@localhost", ID: "source", Author: "bob@localhost"}}, nil)
	if _, err := s.db.Exec("CREATE TRIGGER fail_reply_outbox BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT,'injected'); END;"); err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	if _, err := e.SendMessageWithReply(t.Context(), key, c.ID, "answer", source.ID); err == nil {
		t.Fatal("injected failure accepted")
	}
	if _, err := e.Operation(t.Context(), key); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("failed reply kept receipt", err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM message_replies").Scan(&n); err != nil || n != 0 {
		t.Fatal("failed reply metadata survived", n, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_reply_outbox"); err != nil {
		t.Fatal(err)
	}
	sent, err := e.SendMessageWithReply(t.Context(), key, c.ID, "answer", source.ID)
	if err != nil || sent.Replayed {
		t.Fatal("rolled-back operation not retryable", err)
	}
}

func TestRoomIntentGuardsAcceptanceReplayRevocationAndClaim(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &outboundProtocol{}
	e.SetProtocols(p)
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	if _, err := e.SendMessage(t.Context(), migrationID(), room.ID, "not authorized to enter"); !errors.Is(err, application.ErrRoomNotJoined) {
		t.Fatal("room message implicitly joined", err)
	}
	key := migrationID()
	joined, err := e.JoinRoom(t.Context(), key, room.ID, "", "")
	if err != nil || !joined.Session.Active || joined.Session.IntentID == "" {
		t.Fatal("explicit join intent", err)
	}
	if p.joins != 0 {
		t.Fatal("API joined synchronously before worker")
	}
	sent, err := e.SendMessage(t.Context(), migrationID(), room.ID, "offline queued")
	if err != nil {
		t.Fatal(err)
	}
	left, err := e.LeaveRoom(t.Context(), migrationID(), room.ID, joined.Session.IntentRevisionID)
	if err != nil || left.Session.Active {
		t.Fatal("leave intent", err)
	}
	if _, err = s.ClaimRoomMessage(t.Context(), sent.Message.ID, joined.Session.IntentRevisionID, migrationID(), time.Now()); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("inactive intent claimed queued send", err)
	}
	replay, err := e.JoinRoom(t.Context(), key, room.ID, "", "")
	if err != nil || !replay.Replayed || replay.Session.Active {
		t.Fatal("old join receipt reactivated left room", err)
	}
	if _, err = e.JoinRoom(t.Context(), migrationID(), room.ID, "", joined.Session.IntentRevisionID); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale join defeated leave", err)
	}
	active, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "name", left.Session.IntentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	revoked := domain.RoomSession{AccountID: c.AccountID, ConversationID: room.ID, IntentRevisionID: active.Session.IntentRevisionID, State: "failed", LastErrorCode: "forbidden", Revoked: true, UpdatedAt: time.Now().UTC()}
	if err = e.RoomSessionChanged(t.Context(), revoked); err != nil {
		t.Fatal(err)
	}
	current, err := e.RoomSession(t.Context(), room.ID)
	if err != nil || current.Active || current.State != "failed" || current.IntentRevisionID == active.Session.IntentRevisionID {
		t.Fatal("revocation not durable", err)
	}
	newer, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "new", current.IntentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	_, before, _ := s.Snapshot(t.Context())
	if err = e.RoomSessionChanged(t.Context(), revoked); err != nil {
		t.Fatal(err)
	}
	_, after, _ := s.Snapshot(t.Context())
	now, _ := e.RoomSession(t.Context(), room.ID)
	if !now.Active || now.IntentRevisionID != newer.Session.IntentRevisionID || before != after {
		t.Fatal("old callback revoked or emitted against new intent")
	}
}

func TestRoomLeaveAndQueuedClaimSerialize(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	var expected domain.ID
	for range 10 {
		active, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "name", expected)
		if err != nil {
			t.Fatal(err)
		}
		sent, err := e.SendMessage(t.Context(), migrationID(), room.ID, "claim race")
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		claim := make(chan error, 1)
		leave := make(chan application.RoomIntentResult, 1)
		leaveErr := make(chan error, 1)
		go func() {
			<-start
			_, err := s.ClaimRoomMessage(t.Context(), sent.Message.ID, active.Session.IntentRevisionID, migrationID(), time.Now())
			claim <- err
		}()
		go func() {
			<-start
			result, err := e.LeaveRoom(t.Context(), migrationID(), room.ID, active.Session.IntentRevisionID)
			leave <- result
			leaveErr <- err
		}()
		close(start)
		claimed := <-claim
		left := <-leave
		if err := <-leaveErr; err != nil {
			t.Fatal(err)
		}
		expected = left.Session.IntentRevisionID
		current, err := s.Message(t.Context(), sent.Message.ID)
		if err != nil {
			t.Fatal(err)
		}
		if claimed == nil {
			if current.SendState != "dispatching" {
				t.Fatal("claim succeeded without dispatch state")
			}
		} else if !errors.Is(claimed, application.ErrStateConflict) || current.SendState != "queued" {
			t.Fatal("invalid leave/claim outcome", claimed, current.SendState)
		}
		if current.SendState == "queued" {
			if _, err = s.ClaimRoomMessage(t.Context(), current.ID, active.Session.IntentRevisionID, migrationID(), time.Now()); !errors.Is(err, application.ErrStateConflict) {
				t.Fatal("queued work dispatched after leave", err)
			}
		}
	}
}

func TestExplicitRoomIntentAndFrozenReplySurviveEngineRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	makeEngine := func(store *Store, p *outboundProtocol) *application.Engine {
		e, err := application.New(store, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		e.SetProtocols(p)
		return e
	}
	p := &outboundProtocol{wireReply: true}
	e := makeEngine(s, p)
	registered, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	room := outboundRoom(t, s, registered.Account.ID, "chosen@rooms.localhost")
	_ = outboundRoom(t, s, registered.Account.ID, "saved-only@rooms.localhost")
	source := replySeed(t, s, room, "Original 😀\nlinha", time.Now().UTC(), []domain.MessageReference{{Scope: "room:" + room.Address, ID: "stable-room-ID", Author: room.Address + "/Bob"}}, nil)
	if _, err = e.RoomSession(t.Context(), room.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ConversationDetails(t.Context(), room.ID); err != nil {
		t.Fatal(err)
	}
	if p.joins != 0 {
		t.Fatal("reading saved room implicitly joined")
	}
	intent, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	queued, err := e.SendMessageWithReply(t.Context(), key, room.ID, "Reply offline 🍰", source.ID)
	if err != nil {
		t.Fatal(err)
	}
	stop := startOutboundWorker(t, e)
	time.Sleep(600 * time.Millisecond)
	p.mu.Lock()
	joins, sends := p.joins, len(p.sent)
	p.mu.Unlock()
	if joins != 0 || sends != 0 {
		t.Fatal("offline room intent dispatched")
	}
	stop()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	p2 := &outboundProtocol{connected: true}
	e2 := makeEngine(s, p2)
	current, err := e2.Message(t.Context(), queued.Message.ID)
	if err != nil || !reflect.DeepEqual(current.OutboundReply, queued.Message.OutboundReply) || current.Reply == nil || current.Reply.BodyText != "Reply offline 🍰" {
		t.Fatal("restart lost frozen reply", err)
	}
	stop2 := startOutboundWorker(t, e2)
	awaitOutbound(t, func() bool { p2.mu.Lock(); defer p2.mu.Unlock(); return p2.joins == 1 })
	time.Sleep(600 * time.Millisecond)
	p2.mu.Lock()
	sends = len(p2.sent)
	joining := p2.sessions[room.ID]
	p2.mu.Unlock()
	if sends != 0 || joining.State != "joining" {
		t.Fatal("room sent before own confirmation")
	}
	joining.State = "joined"
	p2.mu.Lock()
	p2.sessions[room.ID] = joining
	p2.mu.Unlock()
	if err = e2.RoomSessionChanged(t.Context(), joining); err != nil {
		t.Fatal(err)
	}
	awaitOutbound(t, func() bool {
		m, err := e2.Message(t.Context(), queued.Message.ID)
		return err == nil && m.SendState == "submitted"
	})
	p2.mu.Lock()
	out := p2.sent[0]
	joinedRooms := len(p2.sessions)
	p2.mu.Unlock()
	if out.Kind != "room" || out.Address != room.Address || out.RoomIntentRevisionID != intent.Session.IntentRevisionID || out.Reply == nil || out.Reply.Target.ID != "stable-room-ID" || joinedRooms != 1 {
		t.Fatal("dispatch lost frozen room plan or joined unselected saved room", out)
	}
	replay, err := e2.SendMessageWithReply(t.Context(), key, room.ID, "Reply offline 🍰", source.ID)
	if err != nil || !replay.Replayed || replay.Message.ID != queued.Message.ID {
		t.Fatal("restart reply receipt not replayable", err)
	}
	left, err := e2.LeaveRoom(t.Context(), migrationID(), room.ID, intent.Session.IntentRevisionID)
	if err != nil || left.Session.Active {
		t.Fatal(err)
	}
	awaitOutbound(t, func() bool { p2.mu.Lock(); defer p2.mu.Unlock(); return p2.leaves == 1 })
	stop2()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	p3 := &outboundProtocol{connected: true, autoJoined: true}
	e3 := makeEngine(s, p3)
	stop3 := startOutboundWorker(t, e3)
	time.Sleep(650 * time.Millisecond)
	stop3()
	if p3.joins != 0 || len(p3.sent) != 0 {
		t.Fatal("explicit leave was lost across restart")
	}
}

func TestRoomObservationEventsDeduplicateAndRejectOldIntent(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &outboundProtocol{}
	e.SetProtocols(p)
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	intent, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "nick", "")
	if err != nil {
		t.Fatal(err)
	}
	observed := domain.RoomSession{AccountID: c.AccountID, ConversationID: room.ID, IntentRevisionID: intent.Session.IntentRevisionID, State: "joined", Nickname: "nick", UpdatedAt: time.Now().UTC()}
	if err = e.RoomSessionChanged(t.Context(), observed); err != nil {
		t.Fatal(err)
	}
	_, before, _ := s.Snapshot(t.Context())
	observed.UpdatedAt = observed.UpdatedAt.Add(time.Second)
	if err = e.RoomSessionChanged(t.Context(), observed); err != nil {
		t.Fatal(err)
	}
	_, after, _ := s.Snapshot(t.Context())
	if before != after {
		t.Fatal("identical own observation duplicated event")
	}
	left, err := e.LeaveRoom(t.Context(), migrationID(), room.ID, intent.Session.IntentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordRoomSession(t.Context(), observed, ""); err != nil {
		t.Fatal(err)
	}
	_, seq, _ := s.Snapshot(t.Context())
	events, err := s.Events(t.Context(), after, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || seq != after+1 || events[0].RoomSession == nil || events[0].RoomSession.IntentRevisionID != left.Session.IntentRevisionID {
		t.Fatal("stale room snapshot entered event stream", events)
	}
}

func TestAmbiguousWireReferenceUsesFrozenTextFallbackOnly(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{wireReply: true})
	ref := domain.MessageReference{Scope: "sender:bob@localhost", ID: "colliding-origin", Author: "bob@localhost"}
	selected := replySeed(t, s, c, "chosen original 🍰", time.Now().UTC(), []domain.MessageReference{ref}, nil)
	_ = replySeed(t, s, c, "different canonical original", time.Now().UTC(), []domain.MessageReference{ref}, nil)
	got, err := e.Message(t.Context(), selected.ID)
	if err != nil || len(got.References) != 0 {
		t.Fatal("ambiguous wire reference entered selector", err)
	}
	reply, err := e.SendMessageWithReply(t.Context(), migrationID(), c.ID, "answer", selected.ID)
	if err != nil || reply.Message.OutboundReply == nil || reply.Message.OutboundReply.Reference != nil || reply.Message.OutboundReply.Excerpt != "chosen original 🍰" || reply.Message.Reply == nil || reply.Message.Reply.MessageID != selected.ID {
		t.Fatal("ambiguity selected a wire target or lost canonical local target", err)
	}
}

func TestWireCollisionRacingAcceptanceDropsStructuredPlanAndReceipt(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &outboundProtocol{wireReply: true}
	e.SetProtocols(p)
	ref := domain.MessageReference{Scope: "sender:bob@localhost", ID: "racing-ID", Author: "bob@localhost"}
	source := replySeed(t, s, c, "chosen", time.Now().UTC(), []domain.MessageReference{ref}, nil)
	p.selectHook = func() {
		_ = replySeed(t, s, c, "concurrent collision", time.Now().UTC(), []domain.MessageReference{ref}, nil)
	}
	key := migrationID()
	sent, err := e.SendMessageWithReply(t.Context(), key, c.ID, "answer", source.ID)
	if err != nil || sent.Message.OutboundReply.Reference != nil {
		t.Fatal("reference became ambiguous before durable acceptance", err)
	}
	p.selectHook = nil
	replay, err := e.SendMessageWithReply(t.Context(), key, c.ID, "answer", source.ID)
	if err != nil || !replay.Replayed || replay.Message.OutboundReply.Reference != nil {
		t.Fatal("receipt preserved rejected wire plan", err)
	}
}

func TestRoomParticipantErrorCannotRejectOwnQueuedEffect(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	if _, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "nick", ""); err != nil {
		t.Fatal(err)
	}
	sent, err := e.SendMessage(t.Context(), migrationID(), room.ID, "owned effect")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TransitionMessage(t.Context(), sent.Message.ID, "queued", "submitted", migrationID(), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	rejection := application.Rejected{AccountID: c.AccountID, MessageID: string(sent.Message.ID), Address: room.Address, FromParticipant: true}
	if err = e.Reject(t.Context(), rejection); err != nil {
		t.Fatal(err)
	}
	current, err := e.Message(t.Context(), sent.Message.ID)
	if err != nil || current.SendState != "submitted" {
		t.Fatal("occupant error rejected own effect", err)
	}
	rejection.FromParticipant = false
	if err = e.Reject(t.Context(), rejection); err != nil {
		t.Fatal(err)
	}
	current, err = e.Message(t.Context(), sent.Message.ID)
	if err != nil || current.SendState != "failed" {
		t.Fatal("bare room rejection ignored", err)
	}
}

func TestRoomIntentMigrationRoundTripKeepsFrozenReplyAndOutbox(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{wireReply: true})
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	if _, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	source := replySeed(t, s, room, "original", time.Now().UTC(), []domain.MessageReference{{Scope: "room:" + room.Address, ID: "stable", Author: room.Address + "/Bob"}}, nil)
	accepted, err := e.SendMessageWithReply(t.Context(), migrationID(), room.ID, "authored 😀", source.ID)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := migratesqlite.WithInstance(s.db, &migratesqlite.Config{})
	if err != nil {
		t.Fatal(err)
	}
	files, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = files.Close() }()
	runner, err := migrate.NewWithInstance("iofs", files, "sqlite", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Migrate(8); err != nil {
		t.Fatal(err)
	}
	if err = runner.Up(); err != nil {
		t.Fatal(err)
	}
	current, err := e.Message(t.Context(), accepted.Message.ID)
	if err != nil || current.ID != accepted.Message.ID || current.RevisionID != accepted.Message.RevisionID || current.OriginalText != accepted.Message.OriginalText || !reflect.DeepEqual(current.OutboundReply, accepted.Message.OutboundReply) || current.Reply.BodyText != "authored 😀" {
		t.Fatal("room migration changed retained reply", err)
	}
	pending, err := s.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 1 || pending[0].ID != current.ID {
		t.Fatal("room migration lost queued effect", err)
	}
	session, err := e.RoomSession(t.Context(), room.ID)
	if err != nil || session.Active {
		t.Fatal("rollback/re-upgrade invented explicit entry intent", err)
	}
	if _, err = s.ClaimRoomMessage(t.Context(), current.ID, migrationID(), migrationID(), time.Now()); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("room without explicit intent claimed retained queue", err)
	}
}

func TestDisconnectedRoomKeepsQueueIntentWithoutClaimingJoining(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &outboundProtocol{}
	e.SetProtocols(p)
	room := outboundRoom(t, s, c.AccountID, "room@rooms.localhost")
	intent, err := e.JoinRoom(t.Context(), migrationID(), room.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	p.sessions = map[domain.ID]domain.RoomSession{room.ID: {AccountID: c.AccountID, ConversationID: room.ID, State: "not_joined", LastErrorCode: "not_connected", UpdatedAt: time.Now().UTC()}}
	observed, err := e.RoomSession(t.Context(), room.ID)
	if err != nil || !observed.Active || observed.State != "not_joined" || observed.LastErrorCode != "not_connected" || observed.IntentRevisionID != intent.Session.IntentRevisionID {
		t.Fatal("offline active intent was presented as joining", observed, err)
	}
	if _, err = e.SendMessage(t.Context(), migrationID(), room.ID, "queue offline"); err != nil {
		t.Fatal("offline active intent lost queue permission", err)
	}
	revoked := domain.RoomSession{AccountID: c.AccountID, ConversationID: room.ID, IntentRevisionID: intent.Session.IntentRevisionID, State: "failed", LastErrorCode: "forbidden", Revoked: true, UpdatedAt: time.Now().UTC()}
	if err = e.RoomSessionChanged(t.Context(), revoked); err != nil {
		t.Fatal(err)
	}
	terminal, err := e.RoomSession(t.Context(), room.ID)
	if err != nil || terminal.Active || terminal.State != "failed" || terminal.LastErrorCode != "forbidden" {
		t.Fatal("disconnect erased durable revocation", terminal, err)
	}
}
