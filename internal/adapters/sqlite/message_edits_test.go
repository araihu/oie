package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type editProtocol struct {
	outboundProtocol
	editMu      sync.Mutex
	outcomes    []string
	edits       []application.Outgoing
	editHook    func(application.Outgoing)
	editSession string
}

func (p *editProtocol) PrepareCorrection(_ context.Context, a domain.Account, _ domain.Conversation, m domain.Message) (domain.OutgoingCorrection, error) {
	p.editMu.Lock()
	defer p.editMu.Unlock()
	session := p.editSession
	if session == "" {
		session = "fixture-session"
	}
	return domain.OutgoingCorrection{TargetMessageID: m.ID, OriginalWireID: m.ProviderID, Scope: "xmpp:correct:sender:" + a.Address, Author: a.Address, SessionID: session}, nil
}
func (p *editProtocol) Send(ctx context.Context, a domain.Account, out application.Outgoing) string {
	if out.Correction == nil {
		return p.outboundProtocol.Send(ctx, a, out)
	}
	p.editMu.Lock()
	p.edits = append(p.edits, out)
	outcome := application.Submitted
	if len(p.outcomes) > 0 {
		outcome = p.outcomes[0]
		p.outcomes = p.outcomes[1:]
	}
	hook := p.editHook
	p.editMu.Unlock()
	if hook != nil {
		hook(out)
	}
	return outcome
}
func ownSubmittedEdit(t *testing.T, s *Store, e *application.Engine, c domain.Conversation, text string) domain.Message {
	t.Helper()
	sent, err := e.SendMessage(t.Context(), migrationID(), c.ID, text)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"dispatching", "submitted"} {
		from := "queued"
		if state == "submitted" {
			from = "dispatching"
		}
		if _, err = s.TransitionMessage(t.Context(), sent.Message.ID, from, state, migrationID(), time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return sent.Message
}
func queuedEdit(t *testing.T, e *application.Engine, id domain.ID, text string) application.MessageEditResult {
	t.Helper()
	target, err := e.MessageEditTarget(t.Context(), id)
	if err != nil || !target.Eligible {
		t.Fatal("target unavailable", target, err)
	}
	got, err := e.EditMessage(t.Context(), migrationID(), id, target.CurrentRevisionID, text)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func finishEdit(t *testing.T, s *Store, id domain.ID, state string) domain.MessageEditCommand {
	t.Helper()
	if _, err := s.ClaimMessageEdit(t.Context(), id, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := s.FinishMessageEdit(t.Context(), id, state, migrationID(), time.Now(), time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSentEditsKeepOriginalImmutableAndRetainOnlySubmittedSources(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &editProtocol{}
	e.SetProtocols(p)
	m := ownSubmittedEdit(t, s, e, c, "Original **literal** café 🧑🏽‍💻")
	before, err := s.MessageSource(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	history := editHistory(t, e, m.ID, "", 20)
	command := queuedEdit(t, e, m.ID, "Correction **one** 😀")
	current, _ := s.Message(t.Context(), m.ID)
	if current.OriginalText != m.OriginalText || current.Edited {
		t.Fatal("queued edit rewrote source", current)
	}
	if got := editHistory(t, e, m.ID, "", 20); !reflect.DeepEqual(got, history) {
		t.Fatal("queued edit changed retained history")
	}
	target, _ := e.MessageEditTarget(t.Context(), m.ID)
	if target.Eligible || target.Reason != "edit_pending" {
		t.Fatal(target)
	}
	replay, err := e.EditMessage(t.Context(), command.Command.ID, m.ID, command.Command.ExpectedRevisionID, command.Command.OriginalText)
	if err != nil || !replay.Replayed || replay.Command.ID != command.Command.ID {
		t.Fatal(replay, err)
	}
	if _, err = e.EditMessage(t.Context(), command.Command.ID, m.ID, command.Command.ExpectedRevisionID, "different body"); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("operation content changed", err)
	}
	finishEdit(t, s, command.Command.ID, "submitted")
	current, err = s.Message(t.Context(), m.ID)
	if err != nil || current.ID != m.ID || current.OriginalText != command.Command.OriginalText || !current.Edited || current.SendState != "submitted" || !current.CreatedAt.Equal(m.CreatedAt) {
		t.Fatal(current, err)
	}
	after, _ := s.MessageSource(t.Context(), m.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("correction changed immutable original")
	}
	history = editHistory(t, e, m.ID, "", 20)
	if len(history.Items) != 2 || !history.Items[0].IsCurrent || !history.Items[1].IsOriginal || history.Items[1].OriginalText != m.OriginalText {
		t.Fatal(history)
	}
	if _, err = e.EditMessage(t.Context(), migrationID(), m.ID, command.Command.ExpectedRevisionID, "stale"); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale head accepted", err)
	}
	second := queuedEdit(t, e, m.ID, "Correction two")
	frozen, _ := s.MessageEdit(t.Context(), second.Command.ID)
	if frozen.Plan.OriginalWireID != string(m.ID) {
		t.Fatal("second edit retargeted correction stanza", frozen)
	}
	finishEdit(t, s, second.Command.ID, "submitted")
	history = editHistory(t, e, m.ID, "", 20)
	if len(history.Items) != 3 || history.Items[0].OriginalText != "Correction two" {
		t.Fatal(history)
	}
	visible, err := e.Messages(t.Context(), c.ID, "", 100)
	if err != nil || len(visible) != 1 {
		t.Fatal("corrections produced duplicate bubbles", visible, err)
	}
}

func TestSentEditTargetNeverSkipsNewerOwnIneligibleSources(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&editProtocol{})
	m := ownSubmittedEdit(t, s, e, c, "Editable own original")
	_, seq, _ := s.Snapshot(t.Context())
	target, err := e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || !target.Eligible || target.MessageID != m.ID {
		t.Fatal(target, err)
	}
	_, after, _ := s.Snapshot(t.Context())
	if seq != after {
		t.Fatal("edit eligibility mutated state")
	}
	queued, err := e.SendMessage(t.Context(), migrationID(), c.ID, "Newer queued original")
	if err != nil {
		t.Fatal(err)
	}
	target, err = e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != m.ID || !target.Eligible {
		t.Fatal("queued text displaced latest sent", target, err)
	}
	target, _ = e.MessageEditTarget(t.Context(), m.ID)
	if !target.Eligible {
		t.Fatal(target)
	}
	if _, err = e.CancelMessage(t.Context(), migrationID(), queued.Message.ID, queued.Message.RevisionID); err != nil {
		t.Fatal(err)
	}
	target, err = e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != m.ID || !target.Eligible {
		t.Fatal("cancelled text displaced latest sent", target, err)
	}
	foreign := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.AccountID, DisplayName: "Alice from another client"}, ProviderID: "foreign-client", IsOwn: true, SendState: "submitted", OriginalText: "Newer imported own original", CreatedAt: time.Now().Add(time.Hour).UTC()}
	if err = s.ReceiveMessage(t.Context(), c, foreign, migrationID()); err != nil {
		t.Fatal(err)
	}
	target, err = e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != m.ID || !target.Eligible {
		t.Fatal("foreign imported source changed engine transmission order", target, err)
	}
	target, err = e.MessageEditTarget(t.Context(), foreign.ID)
	if err != nil || target.Eligible || target.Reason != "foreign_source" {
		t.Fatal("foreign own source became editable", target, err)
	}
}

func TestSentEditAcceptanceIsAtomicAndRacesHaveOneWinner(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&editProtocol{})
	m := ownSubmittedEdit(t, s, e, c, "Unchanged until dispatch")
	target, _ := e.MessageEditTarget(t.Context(), m.ID)
	if _, err := e.EditMessage(t.Context(), m.ID, m.ID, target.CurrentRevisionID, "wire collision"); !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatal("reused original wire id", err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER edit_accept_failure BEFORE INSERT ON message_edit_commands BEGIN SELECT RAISE(ABORT,'injected'); END"); err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	if _, err := e.EditMessage(t.Context(), key, m.ID, target.CurrentRevisionID, "rollback"); err == nil {
		t.Fatal("injected failure accepted")
	}
	if _, err := e.Operation(t.Context(), key); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("partial operation receipt survived", err)
	}
	if _, err := s.db.Exec("DROP TRIGGER edit_accept_failure"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, body := range []string{"winner A", "winner B"} {
		go func(text string) {
			<-start
			_, err := e.EditMessage(t.Context(), migrationID(), m.ID, target.CurrentRevisionID, text)
			results <- err
		}(body)
	}
	close(start)
	a, b := <-results, <-results
	if (a == nil) == (b == nil) || a != nil && !errors.Is(a, application.ErrStateConflict) || b != nil && !errors.Is(b, application.ErrStateConflict) {
		t.Fatal("two editors won", a, b)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM message_edit_commands").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestSentEditClaimRejectsNewerContentAndPreservesOutboxOwnership(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &editProtocol{}
	e.SetProtocols(p)
	m := ownSubmittedEdit(t, s, e, c, "Own original")
	command := queuedEdit(t, e, m.ID, "Queued edit becomes stale")
	account, _ := e.Account(t.Context(), c.AccountID)
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: "other-client-correction", IsOwn: true, OriginalText: "Observed other-client correction", CreatedAt: time.Now().Add(time.Second), Correction: &domain.MessageCorrection{Scope: "xmpp:correct:sender:" + account.Address, Author: account.Address, ID: "other-client-correction", TargetID: string(m.ID)}}); err != nil {
		t.Fatal(err)
	}
	// The incoming source alone cannot establish an original fact for a local
	// source. A new own send also makes the accepted command stale atomically.
	ownSubmittedEdit(t, s, e, c, "Newer own message")
	if _, err := s.ClaimMessageEdit(t.Context(), command.Command.ID, migrationID(), time.Now()); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("stale dispatch claimed", err)
	}
	got, err := e.MessageEdit(t.Context(), command.Command.ID)
	if err != nil || got.State != "failed" {
		t.Fatal(got, err)
	}
	original, _ := s.MessageSource(t.Context(), m.ID)
	if original.SendState != "submitted" || original.OriginalText != m.OriginalText {
		t.Fatal(original)
	}
}

func TestSentEditUnknownAndOwnEchoReconcileWithoutReplayOrDuplicates(t *testing.T) {
	for _, beforeFinish := range []bool{false, true} {
		t.Run(map[bool]string{false: "echo-after-unknown", true: "echo-before-finish"}[beforeFinish], func(t *testing.T) {
			s, e, c := outboxFixture(t)
			e.SetProtocols(&editProtocol{})
			m := ownSubmittedEdit(t, s, e, c, "Original before ambiguous write")
			command := queuedEdit(t, e, m.ID, "Observed corrected source")
			pending, err := s.ClaimMessageEdit(t.Context(), command.Command.ID, migrationID(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if !beforeFinish {
				if _, err = s.FinishMessageEdit(t.Context(), command.Command.ID, "unknown", migrationID(), time.Now(), time.Now(), "unknown_outcome"); err != nil {
					t.Fatal(err)
				}
				target, _ := e.MessageEditTarget(t.Context(), m.ID)
				if target.Reason != "edit_pending" {
					t.Fatal(target)
				}
			}
			echo := application.Incoming{AccountID: c.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: string(command.Command.ID), CanonicalID: command.Command.ID, IsOwn: true, OriginalText: command.Command.OriginalText, CreatedAt: time.Now(), Correction: &domain.MessageCorrection{Scope: pending.Plan.Scope, Author: pending.Plan.Author, ID: string(command.Command.ID), TargetID: pending.Plan.OriginalWireID}}
			if err = e.Receive(t.Context(), echo); err != nil {
				t.Fatal(err)
			}
			if err = e.Receive(t.Context(), echo); err != nil {
				t.Fatal(err)
			}
			got, _ := e.MessageEdit(t.Context(), command.Command.ID)
			if got.State != "submitted" {
				t.Fatal("verified echo did not resolve command", got)
			}
			if beforeFinish {
				if _, err = s.FinishMessageEdit(t.Context(), command.Command.ID, "unknown", migrationID(), time.Now(), time.Now(), "late socket outcome"); !errors.Is(err, application.ErrStateConflict) {
					t.Fatal("late outcome overrode echo", err)
				}
			}
			page := editHistory(t, e, m.ID, "", 20)
			if len(page.Items) != 2 || page.Items[0].OriginalText != command.Command.OriginalText {
				t.Fatal("echo duplicated or broke graph", page)
			}
			// A replayed original uses its immutable physical body after projection.
			originalEcho := application.Incoming{AccountID: c.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: "service-room-original-alias", CanonicalID: m.ID, IsOwn: true, OriginalText: m.OriginalText, CreatedAt: m.CreatedAt, Correction: &domain.MessageCorrection{Scope: pending.Plan.Scope, Author: pending.Plan.Author, ID: string(m.ID)}}
			if err = e.Receive(t.Context(), originalEcho); err != nil {
				t.Fatal(err)
			}
			if got := editHistory(t, e, m.ID, "", 20); len(got.Items) != 2 || got.CurrentRevisionID != page.CurrentRevisionID {
				t.Fatal("original archive replay broke current correction", got)
			}
		})
	}
}

func TestSentEditDispatcherRetriesOnlyKnownUnsentAndFreezesEnvelope(t *testing.T) {
	for _, outcome := range []string{application.NotSent, application.Unknown, application.Failed} {
		t.Run(outcome, func(t *testing.T) {
			s, e, c := outboxFixture(t)
			p := &editProtocol{outcomes: []string{outcome, application.Submitted}}
			p.connected = true
			e.SetProtocols(p)
			m := ownSubmittedEdit(t, s, e, c, "Original transport source")
			command := queuedEdit(t, e, m.ID, "One frozen correction")
			stop := startOutboundWorker(t, e)
			defer stop()
			expected := outcome
			if outcome == application.NotSent {
				expected = application.Submitted
			}
			awaitOutbound(t, func() bool { got, _ := e.MessageEdit(t.Context(), command.Command.ID); return got.State == expected })
			time.Sleep(650 * time.Millisecond)
			p.editMu.Lock()
			sends := append([]application.Outgoing(nil), p.edits...)
			p.editMu.Unlock()
			want := 1
			if outcome == application.NotSent {
				want = 2
			}
			if len(sends) != want {
				t.Fatal("unsafe retry count", len(sends), outcome)
			}
			for _, out := range sends {
				if out.ID != command.Command.ID || out.Correction == nil || out.Correction.OriginalWireID != string(m.ID) || out.OriginalText != command.Command.OriginalText {
					t.Fatal("frozen edit envelope changed", out)
				}
			}
			page := editHistory(t, e, m.ID, "", 20)
			count := 1
			if outcome == application.NotSent {
				count = 2
			}
			if len(page.Items) != count {
				t.Fatal("unconfirmed edit entered content history", page)
			}
		})
	}
}

func TestSentEditInterruptedRestartIsUnknownAndReceiptSurvives(t *testing.T) {
	s, e, c := outboxFixture(t)
	p := &editProtocol{}
	e.SetProtocols(p)
	m := ownSubmittedEdit(t, s, e, c, "Restart original")
	command := queuedEdit(t, e, m.ID, "Interrupted correction")
	if _, err := s.ClaimMessageEdit(t.Context(), command.Command.ID, migrationID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var index int
	var name, path string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&index, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), filepath.Dir(path), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	restarted, err := application.New(reopened, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	p.connected = true
	restarted.SetProtocols(p)
	stop := startOutboundWorker(t, restarted)
	defer stop()
	awaitOutbound(t, func() bool {
		got, _ := restarted.MessageEdit(t.Context(), command.Command.ID)
		return got.State == "unknown"
	})
	replay, err := restarted.EditMessage(t.Context(), command.Command.ID, m.ID, command.Command.ExpectedRevisionID, command.Command.OriginalText)
	if err != nil || !replay.Replayed || replay.Command.State != "unknown" {
		t.Fatal(replay, err)
	}
	time.Sleep(600 * time.Millisecond)
	p.editMu.Lock()
	n := len(p.edits)
	p.editMu.Unlock()
	if n != 0 {
		t.Fatal("restart retried ambiguous correction")
	}
	if page := editHistory(t, restarted, m.ID, "", 20); len(page.Items) != 1 {
		t.Fatal(page)
	}
}

func TestSentEditLateRejectionIsScopedAndDoesNotFailOriginalSend(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&editProtocol{})
	m := ownSubmittedEdit(t, s, e, c, "Original remains submitted")
	command := queuedEdit(t, e, m.ID, "Locally observed corrected source")
	finishEdit(t, s, command.Command.ID, "submitted")
	for _, rejected := range []application.Rejected{{AccountID: migrationID(), MessageID: string(command.Command.ID), FromServer: true}, {AccountID: c.AccountID, MessageID: string(command.Command.ID), Address: "forged@elsewhere"}} {
		if err := e.Reject(t.Context(), rejected); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := e.MessageEdit(t.Context(), command.Command.ID)
	if got.State != "submitted" {
		t.Fatal("unscoped rejection changed edit", got)
	}
	_, seq, _ := s.Snapshot(t.Context())
	if err := e.Reject(t.Context(), application.Rejected{AccountID: c.AccountID, MessageID: string(command.Command.ID), Address: c.Address}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.MessageEdit(t.Context(), command.Command.ID)
	if got.State != "failed" || got.LastErrorCode != "protocol_rejected" {
		t.Fatal(got)
	}
	original, _ := s.MessageSource(t.Context(), m.ID)
	if original.SendState != "submitted" || original.OriginalText != m.OriginalText {
		t.Fatal("edit rejection altered original delivery", original)
	}
	events, err := e.Events(t.Context(), seq)
	if err != nil || len(events) != 1 || events[0].Type != "message.updated" || events[0].Message.ID != m.ID {
		t.Fatal("late rejection lost canonical invalidation", events, err)
	}
	page := editHistory(t, e, m.ID, "", 20)
	if len(page.Items) != 2 {
		t.Fatal("late error erased observed content", page)
	}
	var n int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM outbox WHERE message_id=?", m.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("original outbox ownership changed", n, err)
	}
	if _, err = s.queries.GetMessage(t.Context(), string(command.Command.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.queries.FindProviderMessage(t.Context(), dbgen.FindProviderMessageParams{AccountID: string(c.AccountID), ConversationID: string(c.ID), ProviderID: string(command.Command.ID), IsOwn: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestSentEditsPreserveLocalCausalityWhenAuthoredClockMovesBackwards(t *testing.T) {
	for _, echoFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "local-materialization", true: "echo-first"}[echoFirst], func(t *testing.T) {
			s, e, c := outboxFixture(t)
			e.SetProtocols(&editProtocol{})
			m := ownSubmittedEdit(t, s, e, c, "Original causal source")
			first := queuedEdit(t, e, m.ID, "First correction with later authored time")
			if _, err := s.ClaimMessageEdit(t.Context(), first.Command.ID, migrationID(), time.Now()); err != nil {
				t.Fatal(err)
			}
			later := time.Now().UTC().Add(time.Hour)
			if _, err := s.FinishMessageEdit(t.Context(), first.Command.ID, "submitted", migrationID(), later, later, ""); err != nil {
				t.Fatal(err)
			}
			before := editHistory(t, e, m.ID, "", 20)
			firstRevision := before.CurrentRevisionID
			second := queuedEdit(t, e, m.ID, "Second causally newer correction despite older clock")
			pending, err := s.ClaimMessageEdit(t.Context(), second.Command.ID, migrationID(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			earlier := later.Add(-time.Minute)
			if echoFirst {
				err = e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: string(second.Command.ID), CanonicalID: second.Command.ID, IsOwn: true, OriginalText: second.Command.OriginalText, CreatedAt: earlier, Correction: &domain.MessageCorrection{Scope: pending.Plan.Scope, Author: pending.Plan.Author, ID: string(second.Command.ID), TargetID: pending.Plan.OriginalWireID}})
			} else {
				_, err = s.FinishMessageEdit(t.Context(), second.Command.ID, "submitted", migrationID(), earlier, earlier, "")
			}
			if err != nil {
				t.Fatal(err)
			}
			after := editHistory(t, e, m.ID, "", 20)
			if len(after.Items) != 3 || after.Items[0].IsCurrent || after.Items[0].RevisionID != firstRevision {
				t.Fatal("history timestamp display order changed", after)
			}
			var current domain.MessageEditHistoryItem
			for _, item := range after.Items {
				if item.IsCurrent {
					current = item
				}
			}
			if current.OriginalText != second.Command.OriginalText || !reflect.DeepEqual(current.ParentRevisionIDs, []domain.ID{firstRevision}) {
				t.Fatal("clock order overrode verified local causal parent", after)
			}
			message, err := e.Message(t.Context(), m.ID)
			if err != nil || message.OriginalText != second.Command.OriginalText {
				t.Fatal(message, err)
			}
			if pending.Plan.OriginalWireID != string(m.ID) {
				t.Fatal("local causal edge changed wire root", pending)
			}
		})
	}
}

func TestSentEditLatestUsesEveryOriginalDispatchOrdinalDespiteClockRollback(t *testing.T) {
	s, _, c := outboxFixture(t)
	clock := time.Now().UTC().Add(time.Hour)
	e, err := application.New(s, "local", func() (domain.ID, error) { return migrationID(), nil }, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	e.SetProtocols(&editProtocol{})
	first := ownSubmittedEdit(t, s, e, c, "Original A authored later but sent first")
	clock = clock.Add(-time.Minute)
	second := ownSubmittedEdit(t, s, e, c, "Original B authored earlier but sent second")
	target, err := e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != second.ID || !target.Eligible {
		t.Fatal("wall clock selected an older transmission", target, err)
	}
	if target, err = e.MessageEditTarget(t.Context(), first.ID); err != nil || target.Eligible || target.Reason != "not_latest" {
		t.Fatal(target, err)
	}
	// Attempt A is known unsent, then B is transmitted, then retry A becomes the
	// newest transmission. Every claim must advance the marker, not only first.
	retry, err := e.SendMessage(t.Context(), migrationID(), c.ID, "Retry original A")
	if err != nil {
		t.Fatal(err)
	}
	for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "queued"}} {
		if _, err = s.TransitionMessage(t.Context(), retry.Message.ID, transition[0], transition[1], migrationID(), time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	clock = clock.Add(-time.Minute)
	third := ownSubmittedEdit(t, s, e, c, "Original C sent after A's unsent attempt")
	target, err = e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != third.ID {
		t.Fatal(target, err)
	}
	for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
		if _, err = s.TransitionMessage(t.Context(), retry.Message.ID, transition[0], transition[1], migrationID(), time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	target, err = e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != retry.Message.ID || !target.Eligible {
		t.Fatal("retry did not advance original transmission order", target, err)
	}
	// The newest actual original can be ineligible; ArrowUp must never skip it.
	reply, err := e.SendMessageWithReply(t.Context(), migrationID(), c.ID, "Reply remains unsupported for editing", third.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
		if _, err = s.TransitionMessage(t.Context(), reply.Message.ID, transition[0], transition[1], migrationID(), time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	target, err = e.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != reply.Message.ID || target.Eligible || target.Reason != "unsupported_payload" {
		t.Fatal("latest ineligible payload skipped", target, err)
	}
}

func TestSentEditMigrationFourteenPreservesTransmissionOrderAndData(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&editProtocol{})
	first := ownSubmittedEdit(t, s, e, c, "Preserve source A")
	second := ownSubmittedEdit(t, s, e, c, "Preserve source B")
	engine, sequence, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	history := editHistory(t, e, first.ID, "", 20)
	migrateFixtureToVersion(t, s, 14)
	var index int
	var name, path string
	if err = s.db.QueryRow("PRAGMA database_list").Scan(&index, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), filepath.Dir(path), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	upgraded, err := application.New(reopened, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	upgraded.SetProtocols(&editProtocol{})
	gotID, gotSequence, err := reopened.Snapshot(t.Context())
	if err != nil || gotID != engine || gotSequence != sequence {
		t.Fatal("upgrade changed engine/event identity", gotID, gotSequence, err)
	}
	target, err := upgraded.ConversationEditTarget(t.Context(), c.ID)
	if err != nil || target.MessageID != second.ID || !target.Eligible {
		t.Fatal("dispatch order was not recovered from retained evidence", target, err)
	}
	if got := editHistory(t, upgraded, first.ID, "", 20); !reflect.DeepEqual(got, history) {
		t.Fatal("upgrade altered retained source/history", got)
	}
	var version int
	var dirty bool
	if err = reopened.db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != latestMigration || dirty {
		t.Fatal(version, dirty, err)
	}
	queuedEdit(t, upgraded, second.ID, "New correction after safe schema upgrade")
}

func TestSentEditTerminalFailureSurvivesVerifiedArchiveReplay(t *testing.T) {
	for _, errorFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "echo-then-error", true: "error-then-echo"}[errorFirst], func(t *testing.T) {
			s, e, c := outboxFixture(t)
			e.SetProtocols(&editProtocol{})
			m := ownSubmittedEdit(t, s, e, c, "Terminal error original")
			command := queuedEdit(t, e, m.ID, "Verified attempted correction")
			pending, err := s.ClaimMessageEdit(t.Context(), command.Command.ID, migrationID(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			fail := func() {
				if err := e.Reject(t.Context(), application.Rejected{AccountID: c.AccountID, Address: c.Address, MessageID: string(command.Command.ID)}); err != nil {
					t.Fatal(err)
				}
			}
			if errorFirst {
				fail()
			}
			echo := application.Incoming{AccountID: c.AccountID, Address: c.Address, Kind: c.Kind, ProviderID: string(command.Command.ID), CanonicalID: command.Command.ID, IsOwn: true, OriginalText: command.Command.OriginalText, CreatedAt: time.Now(), Correction: &domain.MessageCorrection{Scope: pending.Plan.Scope, Author: pending.Plan.Author, ID: string(command.Command.ID), TargetID: pending.Plan.OriginalWireID}}
			if err = e.Receive(t.Context(), echo); err != nil {
				t.Fatal(err)
			}
			if !errorFirst {
				fail()
			}
			if err = e.Receive(t.Context(), echo); err != nil {
				t.Fatal(err)
			}
			if history := editHistory(t, e, m.ID, "", 20); len(history.Items) != 2 {
				t.Fatal("terminal failed echo lost canonical observed history", history)
			}
			got, err := e.MessageEdit(t.Context(), command.Command.ID)
			if err != nil || got.State != "failed" || got.LastErrorCode != "protocol_rejected" {
				t.Fatal("archive replay erased terminal delivery failure", got, err)
			}
		})
	}
}
