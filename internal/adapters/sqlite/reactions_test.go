package sqlite

import (
	"context"
	"errors"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"reflect"
	"strings"
	"testing"
	"time"
)

func downReactionMigration(t *testing.T, s *Store) {
	t.Helper()
	for _, name := range []string{"000017_room_archives", "000016_message_reactions"} {
		down, err := migrations.ReadFile("migrations/" + name + ".down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(string(down)); err != nil {
			t.Fatal(err)
		}
	}
}

type reactionProtocol struct {
	outboundProtocol
	max       int
	session   string
	roomActor string
	outcomes  []string
	reactions []application.Outgoing
}

func (p *reactionProtocol) PrepareReaction(_ context.Context, a domain.Account, c domain.Conversation, m domain.Message) (domain.OutgoingReaction, error) {
	session := p.session
	if session == "" {
		session = "reaction-session"
	}
	plan := domain.OutgoingReaction{TargetMessageID: m.ID, Actor: a.Address, SessionID: session, MaxEmojis: p.max, Reference: domain.MessageReference{Scope: "sender:" + a.Address, Author: a.Address, ID: m.ProviderID}}
	if c.Kind == "room" {
		plan.Reference = m.References[0]
		plan.RoomActor, plan.ActorName = p.roomActor, "My room nickname"
	}
	return plan, nil
}
func reactionSnapshot(t *testing.T, e *application.Engine, id domain.ID) domain.MessageReactions {
	t.Helper()
	out, err := e.MessageReactions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func observeReaction(t *testing.T, e *application.Engine, c domain.Conversation, ref domain.MessageReference, actor, wire string, at time.Time, values ...string) {
	t.Helper()
	err := e.ReceiveReaction(t.Context(), application.IncomingReaction{AccountID: c.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: wire, CreatedAt: at, Actor: actor, Target: ref, Emojis: values, IsOwn: actor == "alice@localhost"})
	if err != nil {
		t.Fatal(err)
	}
}
func TestReactionsSetsOrderingDedupAndSourceIsolation(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&reactionProtocol{max: 8})
	m := ownSubmittedEdit(t, s, e, c, "source untouched")
	source, _ := e.Message(t.Context(), m.ID)
	beforeC, _ := e.Conversation(t.Context(), c.ID)
	ref := domain.MessageReference{ID: m.ProviderID}
	now := time.Now().UTC().Add(-time.Minute)
	empty := reactionSnapshot(t, e, m.ID)
	_, before, _ := s.Snapshot(t.Context())
	again := reactionSnapshot(t, e, m.ID)
	_, after, _ := s.Snapshot(t.Context())
	if empty.RevisionID != "" || empty.OwnRevisionID != "" || before != after || !reflect.DeepEqual(empty, again) {
		t.Fatal("GET allocated reaction state")
	}
	observeReaction(t, e, c, ref, "bob@localhost", "new", now.Add(time.Second), "👍", "❤️")
	first := reactionSnapshot(t, e, m.ID)
	if len(first.Items) != 2 {
		t.Fatal(first)
	}
	observeReaction(t, e, c, ref, "bob@localhost", "old", now, "😂")
	if got := reactionSnapshot(t, e, m.ID); !reflect.DeepEqual(got.Items, first.Items) {
		t.Fatal("archive/old event overwrote newer full set", got)
	}
	_, seq, _ := s.Snapshot(t.Context())
	observeReaction(t, e, c, ref, "bob@localhost", "new", now.Add(time.Second), "❤️", "👍")
	_, duplicateSeq, _ := s.Snapshot(t.Context())
	if seq != duplicateSeq {
		t.Fatal("duplicate emitted event")
	}
	key := migrationID()
	accepted, err := e.SetMessageReactions(t.Context(), key, m.ID, "", []string{"👍"})
	if err != nil {
		t.Fatal(err)
	}
	observeReaction(t, e, c, ref, "bob@localhost", "peer-later", now.Add(2*time.Second), "👍")
	if _, err = s.ClaimMessageReaction(t.Context(), accepted.Command.ID, migrationID(), time.Now()); err != nil {
		t.Fatal("peer reaction invalidated own CAS", err)
	}
	if _, err = s.FinishMessageReaction(t.Context(), key, "submitted", migrationID(), time.Now(), time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	current := reactionSnapshot(t, e, m.ID)
	if len(current.Items) != 1 || current.Items[0].Count != 2 || !current.Items[0].Own || current.OwnRevisionID == "" {
		t.Fatal(current)
	}
	replay, err := e.SetMessageReactions(t.Context(), key, m.ID, "", []string{"👍"})
	if err != nil || !replay.Replayed {
		t.Fatal(replay, err)
	}
	if _, err = e.SetMessageReactions(t.Context(), key, m.ID, "", []string{"😂"}); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	clear, err := e.SetMessageReactions(t.Context(), migrationID(), m.ID, current.OwnRevisionID, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMessageReaction(t.Context(), clear.Command.ID, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishMessageReaction(t.Context(), clear.Command.ID, "submitted", migrationID(), time.Now(), time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	current = reactionSnapshot(t, e, m.ID)
	if len(current.OwnEmojis) != 0 || len(current.Items) != 1 || current.Items[0].Count != 1 || current.Items[0].Own {
		t.Fatal("clear erased peer set", current)
	}
	afterSource, _ := e.Message(t.Context(), m.ID)
	afterC, _ := e.Conversation(t.Context(), c.ID)
	if !reflect.DeepEqual(source, afterSource) || !reflect.DeepEqual(beforeC, afterC) {
		t.Fatal("reactions changed source/conversation")
	}
}
func TestReactionsOrphanAmbiguityAndConflictingWireIDs(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&reactionProtocol{max: 8})
	now := time.Now().UTC().Add(-time.Minute)
	wire := "future-source"
	ref := domain.MessageReference{ID: wire}
	observeReaction(t, e, c, ref, "bob@localhost", "reaction-before-source", now, "👍")
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Bob"}, OriginalText: "later retained", CreatedAt: now, ProviderID: wire, SendState: "received", Historical: true, References: []domain.MessageReference{{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: wire}}}
	if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, m.ID); len(got.Items) != 1 {
		t.Fatal("retained orphan not resolved", got)
	}
	observeReaction(t, e, c, ref, "bob@localhost", "reaction-before-source", now, "😂")
	if got := reactionSnapshot(t, e, m.ID); len(got.Items) != 0 {
		t.Fatal("conflicting wire identity applied", got)
	}
	observeReaction(t, e, c, ref, "bob@localhost", "safe-event", now.Add(time.Second), "👍")
	other := m
	other.ID = migrationID()
	other.RevisionID = migrationID()
	other.ProviderID = "different-source"
	other.OriginalText = "different source same claimed ID"
	if err := s.ReceiveMessage(t.Context(), c, other, migrationID()); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, m.ID); len(got.Items) != 0 {
		t.Fatal("ambiguous raw reference was applied", got)
	}
}
func TestReactionPendingRacesUnknownAndLateRejection(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&reactionProtocol{max: 8})
	m := ownSubmittedEdit(t, s, e, c, "source")
	accepted, err := e.SetMessageReactions(t.Context(), migrationID(), m.ID, "", []string{"👍"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.SetMessageReactions(t.Context(), migrationID(), m.ID, "", []string{"😂"}); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("second unresolved command", err)
	}
	if _, err = s.ClaimMessageReaction(t.Context(), accepted.Command.ID, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishMessageReaction(t.Context(), accepted.Command.ID, "unknown", migrationID(), time.Now(), time.Now(), "unknown"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingMessageReactions(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("unknown returned to queue", pending, err)
	}
	observeReaction(t, e, c, domain.MessageReference{ID: m.ProviderID}, "alice@localhost", string(accepted.Command.ID), time.Now(), "👍")
	receipt, err := e.MessageReactionCommand(t.Context(), accepted.Command.ID)
	if err != nil || receipt.State != "submitted" {
		t.Fatal("verified own echo did not resolve unknown", receipt, err)
	}
	current := reactionSnapshot(t, e, m.ID)
	if len(current.OwnEmojis) != 1 {
		t.Fatal(current)
	}
	observeReaction(t, e, c, domain.MessageReference{ID: m.ProviderID}, "alice@localhost", "newer-device-own", time.Now().Add(time.Second), "❤️")
	if err = s.RejectMessageReaction(t.Context(), accepted.Command.ID, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	current = reactionSnapshot(t, e, m.ID)
	if !reflect.DeepEqual(current.OwnEmojis, []string{"❤️"}) {
		t.Fatal("late error erased newer own set", current)
	}
	newer, err := e.SetMessageReactions(t.Context(), migrationID(), m.ID, current.OwnRevisionID, []string{"👍"})
	if err != nil {
		t.Fatal(err)
	}
	observeReaction(t, e, c, domain.MessageReference{ID: m.ProviderID}, "alice@localhost", "another-own-update", time.Now().Add(2*time.Second), "😂")
	if _, err = s.ClaimMessageReaction(t.Context(), newer.Command.ID, migrationID(), time.Now()); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale own update dispatched", err)
	}
}

func (p *reactionProtocol) Send(ctx context.Context, a domain.Account, out application.Outgoing) string {
	if out.Reaction == nil {
		return p.outboundProtocol.Send(ctx, a, out)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reactions = append(p.reactions, out)
	state := application.Submitted
	if len(p.outcomes) > 0 {
		state = p.outcomes[0]
		p.outcomes = p.outcomes[1:]
	}
	return state
}
func TestReactionDispatcherNeverRetriesUnknownAndPreservesIntent(t *testing.T) {
	for _, outcome := range []string{application.NotSent, application.Unknown, application.Failed} {
		t.Run(outcome, func(t *testing.T) {
			s, e, c := outboxFixture(t)
			p := &reactionProtocol{max: 8, outcomes: []string{outcome, application.Submitted}}
			p.connected = true
			e.SetProtocols(p)
			m := ownSubmittedEdit(t, s, e, c, "unchanged source")
			baseline, err := e.Message(t.Context(), m.ID)
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := e.SetMessageReactions(t.Context(), migrationID(), m.ID, "", []string{"👍"})
			if err != nil {
				t.Fatal(err)
			}
			stop := startOutboundWorker(t, e)
			defer stop()
			expected := outcome
			if outcome == application.NotSent {
				expected = application.Submitted
			}
			awaitOutbound(t, func() bool {
				got, _ := e.MessageReactionCommand(t.Context(), accepted.Command.ID)
				return got.State == expected
			})
			time.Sleep(650 * time.Millisecond)
			p.mu.Lock()
			sends := append([]application.Outgoing(nil), p.reactions...)
			p.mu.Unlock()
			want := 1
			if outcome == application.NotSent {
				want = 2
			}
			if len(sends) != want {
				t.Fatal("unsafe reaction redispatch", len(sends), outcome)
			}
			for _, send := range sends {
				if send.ID != accepted.Command.ID || send.Reaction == nil || send.Reaction.TargetMessageID != m.ID || send.Reaction.Reference.ID != m.ProviderID || !reflect.DeepEqual(send.ReactionEmojis, []string{"👍"}) || send.OriginalText != "" {
					t.Fatal("reaction changed frozen payload or became text", send)
				}
			}
			got, _ := e.Message(t.Context(), m.ID)
			if got.RevisionID != baseline.RevisionID || got.OriginalText != baseline.OriginalText {
				t.Fatal("reaction changed source", got)
			}
		})
	}
}
func TestReactionClaimInterruptedRecoveryAndKnownRestrictions(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &reactionProtocol{max: 8}
	e.SetProtocols(p)
	m := ownSubmittedEdit(t, s, e, c, "source")
	accepted, err := e.SetMessageReactions(t.Context(), migrationID(), m.ID, "", []string{"👍"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMessageReaction(t.Context(), accepted.Command.ID, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	stop := startOutboundWorker(t, e)
	awaitOutbound(t, func() bool {
		got, _ := e.MessageReactionCommand(t.Context(), accepted.Command.ID)
		return got.State == "unknown"
	})
	stop()
	p.mu.Lock()
	count := len(p.reactions)
	p.mu.Unlock()
	if count != 0 {
		t.Fatal("interrupted write resent")
	}
	other := starMessage(t, s, c, time.Now())
	p.max = 0
	target := reactionSnapshot(t, e, other.ID)
	if target.Eligible {
		t.Fatal("known zero server limit offered reaction choices", target)
	}
}
func TestReactionAcceptanceAndMaterializationRollback(t *testing.T) {
	for _, where := range []string{"BEFORE INSERT ON message_reaction_commands", "BEFORE INSERT ON message_reaction_observations", "BEFORE INSERT ON changes"} {
		t.Run(where, func(t *testing.T) {
			s, e, c := outboxFixture(t)
			e.SetProtocols(&reactionProtocol{max: 8})
			m := ownSubmittedEdit(t, s, e, c, "source")
			var key domain.ID
			var err error
			if where == "BEFORE INSERT ON message_reaction_commands" {
				key = migrationID()
				if _, err = s.db.Exec("CREATE TRIGGER fail_reaction " + where + " BEGIN SELECT RAISE(ABORT,'injected failure'); END"); err != nil {
					t.Fatal(err)
				}
				if _, err = e.SetMessageReactions(t.Context(), key, m.ID, "", []string{"👍"}); err == nil {
					t.Fatal("acceptance ignored failure")
				}
				if _, err = e.Operation(t.Context(), key); !errors.Is(err, application.ErrNotFound) {
					t.Fatal("partial reaction receipt", err)
				}
				return
			}
			accepted, err := e.SetMessageReactions(t.Context(), migrationID(), m.ID, "", []string{"👍"})
			if err != nil {
				t.Fatal(err)
			}
			key = accepted.Command.ID
			if _, err = s.ClaimMessageReaction(t.Context(), key, migrationID(), time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec("CREATE TRIGGER fail_reaction " + where + " BEGIN SELECT RAISE(ABORT,'injected failure'); END"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishMessageReaction(t.Context(), key, "submitted", migrationID(), time.Now(), time.Now(), ""); err == nil {
				t.Fatal("materialization ignored failure")
			}
			receipt, err := e.MessageReactionCommand(t.Context(), key)
			if err != nil || receipt.State != "dispatching" {
				t.Fatal("partially advanced receipt", receipt, err)
			}
			if got := reactionSnapshot(t, e, m.ID); len(got.OwnEmojis) != 0 || len(got.Items) != 0 {
				t.Fatal("partial reaction materialization", got)
			}
		})
	}
}

func TestReactionActorsRetainVerifiedNamesAvatarsAndNonemptySets(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&reactionProtocol{max: 8})
	m := ownSubmittedEdit(t, s, e, c, "reaction actor identity")
	at := time.Now().UTC().Add(-time.Minute)
	hash := strings.Repeat("a", 64)
	if err := s.SaveAvatar(t.Context(), domain.Avatar{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, Address: "bob@localhost", ImageHash: hash, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := e.ContactsReceived(t.Context(), c.AccountID, []application.ContactInput{{Address: "bob@localhost", DisplayName: "Robert", Subscription: "both"}}); err != nil {
		t.Fatal(err)
	}
	in := application.IncomingReaction{AccountID: c.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: "named-actor", CreatedAt: at, Actor: "bob@localhost", ActorName: "Bob nickname", Target: domain.MessageReference{ID: m.ProviderID}, Emojis: []string{"👍", "❤️"}}
	if err := e.ReceiveReaction(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	_, before, _ := s.Snapshot(t.Context())
	snapshot := reactionSnapshot(t, e, m.ID)
	_, after, _ := s.Snapshot(t.Context())
	if before != after || len(snapshot.Actors) != 1 {
		t.Fatal("actor read mutated state or lost actor", snapshot)
	}
	actor := snapshot.Actors[0]
	if actor.Address != "bob@localhost" || actor.DisplayName != "Robert" || actor.AvatarHash != hash || actor.Own || len(actor.Emojis) != 2 {
		t.Fatal(actor)
	}
	// A same-address profile in another account cannot replace this actor's photo.
	registered, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Label: "Other", Address: "other@localhost"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := registered.Account
	if err := s.SaveAvatar(t.Context(), domain.Avatar{ID: migrationID(), RevisionID: migrationID(), AccountID: foreign.ID, Address: actor.Address, ImageHash: strings.Repeat("b", 64), UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, m.ID).Actors[0]; got.AvatarHash != hash {
		t.Fatal("foreign avatar leaked", got)
	}
	if err := e.ContactsReceived(t.Context(), c.AccountID, []application.ContactInput{}); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, m.ID).Actors[0]; got.DisplayName != "Bob nickname" {
		t.Fatal("inactive contact name superseded observed actor", got)
	}
	// Clearing removes the actor from the roster without deleting the source.
	in.ProviderID = "named-clear"
	in.CreatedAt = at.Add(time.Second)
	in.Emojis = []string{}
	if err := e.ReceiveReaction(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	cleared := reactionSnapshot(t, e, m.ID)
	if len(cleared.Actors) != 0 || len(cleared.Items) != 0 {
		t.Fatal("empty actor remained", cleared)
	}
}

func TestSemianonymousReactionProjectionAndCommandEcho(t *testing.T) {
	s, e, base := outboxFixture(t)
	own := domain.RoomReactionActorPrefix + strings.Repeat("a", 64)
	peer := domain.RoomReactionActorPrefix + strings.Repeat("b", 64)
	p := &reactionProtocol{max: 8, roomActor: own}
	p.autoJoined = true
	e.SetProtocols(p)
	ctx := t.Context()
	c := outboundRoom(t, s, base.AccountID, "room@rooms.localhost")
	joined, err := e.JoinRoom(ctx, migrationID(), c.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.JoinRoom(ctx, a, c, "My room nickname", joined.Session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Minute)
	ref := domain.MessageReference{ID: "room-original", Scope: "room:" + c.Address, Author: c.Address + "/Bob"}
	m := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "retained source", CreatedAt: at, Historical: true, ProviderID: "source-provider-id", References: []domain.MessageReference{ref}, SendState: "received"}
	if err = s.ReceiveMessage(ctx, c, m, migrationID()); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveAvatar(ctx, domain.Avatar{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, Address: peer, ImageHash: strings.Repeat("c", 64), UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	ref.Author = ""
	incoming := application.IncomingReaction{AccountID: c.AccountID, Address: c.Address, Kind: "room", ProviderID: "peer-first", CreatedAt: at, Actor: peer, ActorName: "Bob", Target: ref, Emojis: []string{"👍"}}
	if err = e.ReceiveReaction(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	incoming.ProviderID, incoming.ActorName, incoming.CreatedAt = "peer-renamed", "Robert", at.Add(time.Second)
	if err = e.ReceiveReaction(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	if _, err = e.SetMessageReactions(ctx, key, m.ID, "", []string{"👍"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMessageReaction(ctx, key, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishMessageReaction(ctx, key, "submitted", migrationID(), time.Now(), time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	incoming.ProviderID, incoming.Actor, incoming.ActorName, incoming.IsOwn, incoming.CreatedAt = string(key), own, "My room nickname", true, time.Now()
	if err = e.ReceiveReaction(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	got := reactionSnapshot(t, e, m.ID)
	if len(got.Items) != 1 || got.Items[0].Count != 2 || !got.Items[0].Own || len(got.Actors) != 2 ||
		got.Actors[0].Address != own || got.Actors[1].Address != peer || got.Actors[1].DisplayName != "Robert" || got.Actors[1].AvatarHash != "" {
		t.Fatal("own echo duplicated the set, or room actor lost its verified name or privacy", got)
	}
	clear := migrationID()
	if _, err = e.SetMessageReactions(ctx, clear, m.ID, got.OwnRevisionID, []string{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMessageReaction(ctx, clear, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	incoming.ProviderID, incoming.Emojis, incoming.CreatedAt = string(clear), []string{}, time.Now()
	if err = e.ReceiveReaction(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	cmd, err := e.MessageReactionCommand(ctx, clear)
	if err != nil || cmd.State != "submitted" {
		t.Fatal("room echo did not reconcile clear", cmd, err)
	}
	got = reactionSnapshot(t, e, m.ID)
	if len(got.OwnEmojis) != 0 || len(got.Actors) != 1 || got.Actors[0].Address != peer || len(got.Items) != 1 || got.Items[0].Count != 1 {
		t.Fatal("clear affected a different room occupant", got)
	}
}
