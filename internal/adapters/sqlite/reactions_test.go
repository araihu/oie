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
	for _, name := range []string{"000020_room_reaction_retained_identity", "000019_room_reaction_identity_mode", "000018_room_reaction_reconciliation", "000017_room_archives", "000016_message_reactions"} {
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
	public := incoming
	public.Actor, public.ProviderID = "bob@localhost", "public-reaction"
	if err := e.SetRoomReactionPrivacy(ctx, c.AccountID, c.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsNonanonymous, Public: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.ReceiveReaction(ctx, public); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, m.ID); len(got.Actors) != 1 || got.Actors[0].Address != public.Actor {
		t.Fatal("verified public actor not visible", got)
	}
	if err := e.SetRoomReactionPrivacy(ctx, c.AccountID, c.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsSemianonymous}); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, m.ID); len(got.Actors) != 0 || len(got.Items) != 0 {
		t.Fatal("room configuration change left a legacy real address or count", got)
	}
	clearPublic := incoming
	clearPublic.ProviderID, clearPublic.Emojis = "occupant-clear", []string{}
	if err := e.ReceiveReaction(ctx, clearPublic); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, m.ID); len(got.Actors) != 0 || len(got.Items) != 0 {
		t.Fatal("clearing as an occupant exposed the prior real address", got)
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
	incoming.ProviderID, incoming.Actor, incoming.ActorName, incoming.IsOwn, incoming.CreatedAt = "room-reaction:own-add", own, "My room nickname", true, time.Now()
	incoming.CommandEchoID = string(key)
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
	incoming.ProviderID, incoming.Emojis, incoming.CreatedAt = "room-reaction:own-clear", []string{}, time.Now()
	incoming.CommandEchoID = string(clear)
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

func TestRoomArchiveReconcilesReceiptTimeAndPreservesEqualTimestampOrder(t *testing.T) {
	s, e, base := outboxFixture(t)
	e.SetProtocols(&reactionProtocol{max: 8})
	ctx := t.Context()
	c := outboundRoom(t, s, base.AccountID, "room@rooms.localhost")
	stamp := time.Now().UTC().Add(-time.Minute)
	ref := domain.MessageReference{ID: "source", Scope: "room:" + c.Address}
	msg := domain.Message{
		ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "source",
		CreatedAt: stamp.Add(-time.Second), Historical: true, ProviderID: "source",
		References: []domain.MessageReference{ref}, SendState: "received",
	}
	if err := s.ReceiveMessage(ctx, c, msg, migrationID()); err != nil {
		t.Fatal(err)
	}
	add := application.IncomingReaction{
		AccountID: c.AccountID, Address: c.Address, Kind: "room", ProviderID: "room-reaction:z-add",
		Actor: domain.RoomReactionActorPrefix + strings.Repeat("a", 64), ActorName: "Bob", Target: ref,
		CreatedAt: stamp.Add(time.Second), Emojis: []string{"👍"},
	}
	clear := add
	clear.ProviderID, clear.Emojis = "room-reaction:a-clear", []string{}
	clear.Historical, clear.ArchiveScope, clear.ArchiveID = true, c.Address, "a-clear"
	clear.CreatedAt = stamp
	for _, in := range []application.IncomingReaction{add, clear} {
		if err := e.ReceiveReaction(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 1 {
		t.Fatal("expected the original live receipt time to win before reconciliation", got)
	}
	add.Historical, add.ArchiveScope, add.ArchiveID = true, c.Address, "z-add"
	add.CreatedAt = stamp
	add.ArchiveLater = []string{clear.ArchiveID}
	clear.ArchiveEarlier = []string{add.ArchiveID}
	for range 2 {
		for _, in := range []application.IncomingReaction{add, clear} {
			if err := e.ReceiveReaction(ctx, in); err != nil {
				t.Fatal(err)
			}
		}
		if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 0 || len(got.Actors) != 0 {
			t.Fatal("the later archived clear lost to receipt time or lexicographic ID", got)
		}
	}
}

func TestLateRoomReactionEchoKeepsQueuedClearValid(t *testing.T) {
	s, e, base := outboxFixture(t)
	own := domain.RoomReactionActorPrefix + strings.Repeat("a", 64)
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

	ref.Author = ""
	add := migrationID()
	if _, err := e.SetMessageReactions(ctx, add, m.ID, "", []string{"👍"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimMessageReaction(ctx, add, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishMessageReaction(ctx, add, "submitted", migrationID(), time.Now(), time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	before := reactionSnapshot(t, e, m.ID)
	clear := migrationID()
	if _, err := e.SetMessageReactions(ctx, clear, m.ID, before.OwnRevisionID, []string{}); err != nil {
		t.Fatal(err)
	}
	echo := application.IncomingReaction{
		AccountID: c.AccountID, Address: c.Address, Kind: "room", ProviderID: "room-reaction:stable-own-add", CommandEchoID: string(add),
		CreatedAt: time.Now(), IsOwn: true, Actor: own, ActorName: "My room nickname", Target: ref, Emojis: []string{"👍"},
	}
	if err := e.ReceiveReaction(ctx, echo); err != nil {
		t.Fatal(err)
	}
	after := reactionSnapshot(t, e, m.ID)
	if _, err := s.ClaimMessageReaction(ctx, clear, migrationID(), time.Now()); err != nil {
		t.Fatalf("late echo changed own revision %s -> %s while own emojis stayed %v -> %v, queued clear failed: %v", before.OwnRevisionID, after.OwnRevisionID, before.OwnEmojis, after.OwnEmojis, err)
	}
}

func TestLaterLegacyRoomReactionClearBeatsEarlierArchiveAdd(t *testing.T) {
	s, e, base := outboxFixture(t)
	e.SetProtocols(&reactionProtocol{max: 8})
	ctx := t.Context()
	c := outboundRoom(t, s, base.AccountID, "room@rooms.localhost")
	stamp := time.Now().UTC().Add(-time.Minute)
	ref := domain.MessageReference{ID: "source", Scope: "room:" + c.Address}
	msg := domain.Message{
		ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "source",
		CreatedAt: stamp.Add(-time.Second), Historical: true, ProviderID: "source",
		References: []domain.MessageReference{ref}, SendState: "received",
	}
	if err := s.ReceiveMessage(ctx, c, msg, migrationID()); err != nil {
		t.Fatal(err)
	}

	add := application.IncomingReaction{
		AccountID: c.AccountID, Address: c.Address, Kind: "room", ProviderID: "legacy-client-add", ArchiveID: "room-archive-add", ArchiveScope: c.Address,
		Historical: true, CreatedAt: stamp, Actor: domain.RoomReactionActorPrefix + strings.Repeat("a", 64),
		ActorName: "Bob", Target: ref, Emojis: []string{"👍"},
	}
	clear := add
	clear.ProviderID, clear.ArchiveID, clear.ArchiveScope, clear.Historical = "legacy-client-clear", "", "", false
	clear.CreatedAt, clear.Emojis = stamp.Add(time.Second), []string{}
	for _, in := range []application.IncomingReaction{add, clear} {
		if err := e.ReceiveReaction(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 0 {
		t.Fatalf("older archived add resurfaced despite later retained v17 live clear: %+v", got.Items)
	}
}

func roomReactionReplayFixture(t *testing.T) (*Store, *application.Engine, domain.Message, application.IncomingReaction, application.IncomingReaction) {
	s, e, base := outboxFixture(t)
	e.SetProtocols(&reactionProtocol{max: 8})
	c := outboundRoom(t, s, base.AccountID, "room@rooms.localhost")
	at := time.Now().UTC().Add(-time.Minute)
	ref := domain.MessageReference{ID: "source", Scope: "room:" + c.Address}
	msg := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "source", CreatedAt: at.Add(-time.Second), Historical: true, ProviderID: "source", References: []domain.MessageReference{ref}, SendState: "received"}
	if err := s.ReceiveMessage(t.Context(), c, msg, migrationID()); err != nil {
		t.Fatal(err)
	}
	add := application.IncomingReaction{AccountID: c.AccountID, Address: c.Address, Kind: "room", ProviderID: "z-add", ArchiveScope: c.Address, ArchiveID: "mam-add", CreatedAt: at, Historical: true, Target: ref, Actor: domain.RoomReactionActorPrefix + strings.Repeat("a", 64), ActorName: "Bob", Emojis: []string{"👍"}}
	clear := add
	clear.ProviderID, clear.ArchiveID, clear.Emojis = "a-clear", "mam-clear", []string{}
	return s, e, msg, add, clear
}

func TestRoomReactionReplayRepairsV17BackwardPageOrder(t *testing.T) {
	s, e, msg, add, clear := roomReactionReplayFixture(t)
	// V17 ingested newest backward pages first, with no ordering hints.
	for _, in := range []application.IncomingReaction{clear, add} {
		if err := e.ReceiveReaction(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	// Downgrade the scratch database to the actual V17 migration state.
	for _, name := range []string{"000020_room_reaction_retained_identity", "000019_room_reaction_identity_mode", "000018_room_reaction_reconciliation"} {
		down, err := migrations.ReadFile("migrations/" + name + ".down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(string(down)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("UPDATE schema_migrations SET version=17"); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(t.Context(), s.db); err != nil {
		t.Fatal(err)
	}
	// Read both events together, in the authoritative order from the archive.
	add.ProviderID, clear.ProviderID = "room-reaction:z-add", "room-reaction:a-clear"
	add.ArchiveLater = []string{clear.ArchiveID}
	clear.ArchiveEarlier = []string{add.ArchiveID}
	for range 2 {
		for _, in := range []application.IncomingReaction{add, clear} {
			if err := e.ReceiveReaction(t.Context(), in); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 0 {
		t.Fatalf("an older addition still overrides the newer clear after full authoritative replay: %v", got.Items)
	}
}

func TestRoomReactionReplayRepairsSeparatePages(t *testing.T) {
	_, e, msg, add, clear := roomReactionReplayFixture(t)
	add.ProviderID, clear.ProviderID = "room-reaction:z-add", "room-reaction:a-clear"
	// A context or recent archive read sees the clear. A separate bounded
	// forward replay contains only the earlier add at the same timestamp.
	for _, in := range []application.IncomingReaction{clear, add} {
		if err := e.ReceiveReaction(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	clear.ArchiveEarlier = []string{add.ArchiveID}
	for _, in := range []application.IncomingReaction{add, clear} {
		if err := e.ReceiveReaction(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 0 {
		t.Fatalf("an older addition still overrides the newer clear after full authoritative replay: %v", got.Items)
	}
}

func TestRoomReactionModeRoundTripKeepsClearedPeerHidden(t *testing.T) {
	_, e, msg, add, clear := roomReactionReplayFixture(t)
	ctx := t.Context()
	add.ArchiveID, add.ArchiveScope, add.Historical = "", "", false
	add.Actor, add.ProviderID = "bob@localhost", "room-reaction:public-add"
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsNonanonymous, Public: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.ReceiveReaction(ctx, add); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 1 {
		t.Fatal("fixture public reaction missing", got.Items)
	}
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsSemianonymous}); err != nil {
		t.Fatal(err)
	}
	clear.CreatedAt = add.CreatedAt.Add(time.Second)
	clear.ProviderID = "room-reaction:private-clear"
	if err := e.ReceiveReaction(ctx, clear); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 0 {
		t.Fatal("prior public reaction visible while private", got.Items)
	}
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsNonanonymous, Public: true}); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 0 {
		t.Fatalf("the previously cleared reaction has resurfaced in public mode: %v", got.Items)
	}
}

func TestArchivedRoomReactionBeforeModeDiscoveryRemainsVisible(t *testing.T) {
	_, e, msg, add, _ := roomReactionReplayFixture(t)
	ctx := t.Context()
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsNonanonymous, Public: true}); err != nil {
		t.Fatal(err)
	}
	// The server has already switched to semi-anonymous and this event has
	// the verified occupant ID, but the client observes the switch later.
	add.CreatedAt = time.Now().UTC()
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsSemianonymous}); err != nil {
		t.Fatal(err)
	}
	if err := e.ReceiveReaction(ctx, add); err != nil {
		t.Fatal(err)
	}
	if got := reactionSnapshot(t, e, msg.ID); len(got.Items) != 1 {
		t.Fatalf("valid reaction in the new identity mode is permanently hidden after replay: %v", got.Items)
	}
}

func TestArchivedRoomReactionAliasKeepsQueuedClearValid(t *testing.T) {
	s, e, base := outboxFixture(t)
	own := domain.RoomReactionActorPrefix + strings.Repeat("a", 64)
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
	if _, err = p.JoinRoom(ctx, a, c, "Oie", joined.Session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Minute)
	ref := domain.MessageReference{ID: "room-source", Scope: "room:" + c.Address, Author: c.Address + "/Bob"}
	msg := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, OriginalText: "source", CreatedAt: at, Historical: true, ProviderID: "source", References: []domain.MessageReference{ref}, SendState: "received"}
	if err = s.ReceiveMessage(ctx, c, msg, migrationID()); err != nil {
		t.Fatal(err)
	}
	add := migrationID()
	if _, err = e.SetMessageReactions(ctx, add, msg.ID, "", []string{"👍"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMessageReaction(ctx, add, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishMessageReaction(ctx, add, "submitted", migrationID(), time.Now(), time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	// A V17/V18 copy of this same operation lacked our command alias because
	// the room replaced the stanza ID. The verified origin in a replay
	// becomes recognizable once CommandEchoID is available.
	ref.Author = ""
	echo := application.IncomingReaction{AccountID: c.AccountID, Address: c.Address, Kind: "room", ProviderID: "rewritten-wire-id", ArchiveID: "mam-echo", ArchiveScope: c.Address, Historical: true, CreatedAt: at.Add(time.Second), IsOwn: true, Actor: own, ActorName: "Oie", Target: ref, Emojis: []string{"👍"}}
	if err = e.ReceiveReaction(ctx, echo); err != nil {
		t.Fatal(err)
	}
	before := reactionSnapshot(t, e, msg.ID)
	clear := migrationID()
	if _, err = e.SetMessageReactions(ctx, clear, msg.ID, before.OwnRevisionID, []string{}); err != nil {
		t.Fatal(err)
	}
	echo.ProviderID, echo.CommandEchoID = "room-reaction:stable-echo", string(add)
	if err = e.ReceiveReaction(ctx, echo); err != nil {
		t.Fatal(err)
	}
	after := reactionSnapshot(t, e, msg.ID)
	if _, err = s.ClaimMessageReaction(ctx, clear, migrationID(), time.Now()); err != nil {
		t.Fatalf("enriching duplicate aliases changed own revision %s to %s without changing emojis %v -> %v, clear failed: %v", before.OwnRevisionID, after.OwnRevisionID, before.OwnEmojis, after.OwnEmojis, err)
	}
}

func TestRoomReactionUpgradeRetiresMixedLegacyPeerSets(t *testing.T) {
	s, e, msg, add, clear := roomReactionReplayFixture(t)
	ctx := t.Context()
	public := application.RoomReactionPrivacy{Mode: application.RoomReactionsNonanonymous, Public: true}
	private := application.RoomReactionPrivacy{Mode: application.RoomReactionsSemianonymous}
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, public); err != nil {
		t.Fatal(err)
	}
	add.Actor, add.ProviderID, add.ArchiveID, add.ArchiveScope, add.Historical = "bob@localhost", "public-add", "", "", false
	if err := e.ReceiveReaction(ctx, add); err != nil {
		t.Fatal(err)
	}
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, private); err != nil {
		t.Fatal(err)
	}
	clear.ProviderID, clear.ArchiveID, clear.ArchiveScope, clear.Historical = "private-clear", "", "", false
	clear.CreatedAt = add.CreatedAt.Add(time.Second)
	if err := e.ReceiveReaction(ctx, clear); err != nil {
		t.Fatal(err)
	}
	if err := e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, public); err != nil {
		t.Fatal(err)
	}
	// v18 retained observations without their identity-mode epochs. It also
	// persisted the latest non-anonymous mode as a currently public flag.
	for _, name := range []string{"000020_room_reaction_retained_identity", "000019_room_reaction_identity_mode"} {
		down, err := migrations.ReadFile("migrations/" + name + ".down.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(string(down)); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if _, err = s.db.Exec("UPDATE schema_migrations SET version=18"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, s.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// Fresh authoritative discovery after joining.
	if err = s2.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, public); err != nil {
		t.Fatal(err)
	}
	got, err := s2.ReadMessageReactions(ctx, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Snapshot.Items) != 0 {
		t.Fatalf("upgrade restored a reaction that the same participant had cleared: %v", got.Snapshot.Items)
	}
}

func TestLeaveIntentImmediatelyWithdrawsPublicReactionActors(t *testing.T) {
	_, e, msg, add, _ := roomReactionReplayFixture(t)
	ctx := t.Context()
	m, err := e.Message(ctx, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.Conversation(ctx, m.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	p := &reactionProtocol{outboundProtocol: outboundProtocol{autoJoined: true}, max: 8}
	e.SetProtocols(p)
	joined, err := e.JoinRoom(ctx, migrationID(), c.ID, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.JoinRoom(ctx, a, c, "alice", joined.Session.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	if err = e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsNonanonymous, Public: true}); err != nil {
		t.Fatal(err)
	}
	add.Actor, add.ArchiveID, add.ArchiveScope, add.Historical = "bob@localhost", "", "", false
	if err = e.ReceiveReaction(ctx, add); err != nil {
		t.Fatal(err)
	}
	before := reactionSnapshot(t, e, msg.ID)
	if !before.Eligible || len(before.Actors) != 1 {
		t.Fatalf("fixture did not start public and joined: %+v", before)
	}
	left, err := e.LeaveRoom(ctx, migrationID(), c.ID, joined.Session.IntentRevisionID)
	if err != nil || left.Session.Active || left.Session.State != "not_joined" {
		t.Fatalf("leave failed: %+v %v", left, err)
	}
	if err = e.SetRoomReactionPrivacy(ctx, add.AccountID, add.Address, application.RoomReactionPrivacy{Mode: application.RoomReactionsNonanonymous, Public: true}); err != nil {
		t.Fatal(err)
	}
	after := reactionSnapshot(t, e, msg.ID)
	if after.Eligible || after.Reason != "session_changed" {
		t.Fatalf("state did not leave: %+v", after)
	}
	if len(after.Actors) != 0 {
		t.Fatalf("API response retains real actor after accepting leave intent: %+v", after.Actors)
	}
}
