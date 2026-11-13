package sqlite

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func receiveCorrection(t *testing.T, e *application.Engine, c domain.Conversation, id, text, target, scope, author string, at time.Time) {
	t.Helper()
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: id, OriginalText: text, SenderName: "Displayed Bob", Kind: c.Kind, Historical: true, CreatedAt: at, Correction: &domain.MessageCorrection{Scope: scope, Author: author, ID: id, TargetID: target}}); err != nil {
		t.Fatal(err)
	}
}
func correctionMessages(t *testing.T, e *application.Engine, c domain.Conversation) []domain.Message {
	t.Helper()
	items, err := e.Messages(t.Context(), c.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestCorrectionGraphKeepsSourcesAndPublishesOnlyCanonicalUpdates(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().Add(-time.Hour).UTC()
	receiveCorrection(t, e, c, "first", "original", "", "proof:bob", "bob", at)
	original := correctionMessages(t, e, c)[0]
	rawBefore, err := s.queries.GetMessage(t.Context(), string(original.ID))
	if err != nil {
		t.Fatal(err)
	}
	_, before, _ := s.Snapshot(t.Context())
	receiveCorrection(t, e, c, "edit", "corrected 😀", "first", "proof:bob", "bob", at.Add(time.Second))
	items := correctionMessages(t, e, c)
	if len(items) != 1 || items[0].ID != original.ID || items[0].OriginalText != "corrected 😀" || !items[0].Edited || !items[0].CreatedAt.Equal(at) {
		t.Fatal("correction replaced stable source identity", items)
	}
	rawAfter, err := s.queries.GetMessage(t.Context(), string(original.ID))
	if err != nil || rawBefore.Payload != rawAfter.Payload || rawBefore.RevisionID != rawAfter.RevisionID {
		t.Fatal("immutable original source was rewritten", err)
	}
	events, err := s.Events(t.Context(), before, 100)
	if err != nil {
		t.Fatal(err)
	}
	updates := 0
	for _, event := range events {
		if event.Message != nil {
			updates++
			if event.Message.ID != original.ID || !event.Message.Edited || event.Message.OriginalText != "corrected 😀" {
				t.Fatal("correction event recreated hidden alias", event)
			}
		}
	}
	if updates == 0 {
		t.Fatal("correction emitted no invalidation")
	}
	_, before, _ = s.Snapshot(t.Context())
	head := items[0].RevisionID
	receiveCorrection(t, e, c, "edit", "corrected 😀", "first", "proof:bob", "bob", at.Add(time.Hour))
	_, after, _ := s.Snapshot(t.Context())
	if after != before || correctionMessages(t, e, c)[0].RevisionID != head {
		t.Fatal("replay changed correction history")
	}
	receiveCorrection(t, e, c, "descendant", "clock-skew descendant", "edit", "proof:bob", "bob", at.Add(-time.Hour))
	items = correctionMessages(t, e, c)
	if len(items) != 1 || items[0].OriginalText != "clock-skew descendant" {
		t.Fatal("ancestor timestamp defeated descendant", items)
	}
	pending, err := s.PendingMessages(t.Context(), time.Now())
	if err != nil || len(pending) != 0 {
		t.Fatal("received correction created send intent", err)
	}
}

func TestLateOriginalAliasesPreserveFrozenOutgoingReply(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().Add(-time.Hour).UTC()
	receiveCorrection(t, e, c, "third", "latest", "second", "proof:bob", "bob", at.Add(3*time.Second))
	third := correctionMessages(t, e, c)[0]
	receiveCorrection(t, e, c, "second", "middle", "first", "proof:bob", "bob", at.Add(2*time.Second))
	middle := correctionMessages(t, e, c)[0]
	if middle.ID == third.ID || middle.OriginalText != "latest" {
		t.Fatal("intermediate original did not resolve orphan", middle)
	}
	sent, err := e.SendMessageWithReply(t.Context(), migrationID(), c.ID, "queued authored", middle.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.queries.GetMessage(t.Context(), string(sent.Message.ID))
	if err != nil {
		t.Fatal(err)
	}
	receiveCorrection(t, e, c, "first", "original", "", "proof:bob", "bob", at)
	rootRow, err := s.queries.FindProviderMessage(t.Context(), dbgen.FindProviderMessageParams{AccountID: string(c.AccountID), ConversationID: string(c.ID), ProviderID: "first", IsOwn: 0})
	if err != nil {
		t.Fatal(err)
	}
	rootID := domain.ID(rootRow.ID)
	for _, alias := range []domain.ID{third.ID, middle.ID} {
		current, err := e.Message(t.Context(), alias)
		if err != nil || current.ID != rootID || current.OriginalText != "latest" {
			t.Fatal("old URL did not resolve canonical source", alias, err)
		}
		window, err := e.MessageContext(t.Context(), c.ID, alias, 20)
		if err != nil || window.TargetID != rootID || window.RequestedTargetID != alias {
			t.Fatal("context lost scoped alias proof", window, err)
		}
	}
	after, err := s.queries.GetMessage(t.Context(), string(sent.Message.ID))
	if err != nil || after.Payload != frozen.Payload || after.RevisionID != frozen.RevisionID {
		t.Fatal("correction rewrote accepted outbox", err)
	}
	current, err := e.Message(t.Context(), sent.Message.ID)
	if err != nil || current.OutboundReply.TargetID != middle.ID || current.Reply.MessageID != rootID || current.Reply.BodyText != "queued authored" {
		t.Fatal("frozen reply target or navigation changed incorrectly", current, err)
	}
}

func TestCorrectionProofCollisionAndCyclesRemainSeparate(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().UTC()
	receiveCorrection(t, e, c, "first", "original", "", "proof:bob", "bob", at)
	receiveCorrection(t, e, c, "edit", "corrected", "first", "proof:bob", "bob", at.Add(time.Second))
	if len(correctionMessages(t, e, c)) != 1 {
		t.Fatal("valid fixture did not merge")
	}
	receiveCorrection(t, e, c, "first", "colliding different source body", "", "proof:bob", "bob", at)
	items := correctionMessages(t, e, c)
	if len(items) != 2 {
		t.Fatal("source-body collision retained unsafe merge", items)
	}
	for _, m := range items {
		if m.OriginalText == "colliding different source body" {
			t.Fatal("collision overwrote retained text")
		}
	}
	_, before, _ := s.Snapshot(t.Context())
	receiveCorrection(t, e, c, "first", "colliding different source body", "", "proof:bob", "bob", at)
	_, after, _ := s.Snapshot(t.Context())
	if before != after {
		t.Fatal("collision replay emitted another event")
	}
	other := outboundRoom(t, s, c.AccountID, "cycle@rooms.localhost")
	receiveCorrection(t, e, other, "a", "a text", "b", "room-proof", "room/nick", at)
	receiveCorrection(t, e, other, "b", "b text", "a", "room-proof", "room/nick", at)
	if got := correctionMessages(t, e, other); len(got) != 2 {
		t.Fatal("cyclic edits hid retained originals", got)
	}
	foreign := outboundRoom(t, s, c.AccountID, "foreign@rooms.localhost")
	receiveCorrection(t, e, foreign, "a", "author one", "", "room-proof", "room/one", at)
	receiveCorrection(t, e, foreign, "b", "author two", "a", "room-proof", "room/two", at.Add(time.Second))
	if len(correctionMessages(t, e, foreign)) != 2 {
		t.Fatal("display-name equality crossed author proof")
	}
}

func TestReplyReferenceAliasesCannotHideAConflictingThirdTarget(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().UTC()
	receiveCorrection(t, e, c, "first", "original", "", "proof:bob", "bob", at)
	receiveCorrection(t, e, c, "edit", "corrected", "first", "proof:bob", "bob", at.Add(time.Second))
	root := correctionMessages(t, e, c)[0]
	alias, err := s.queries.FindProviderMessage(t.Context(), dbgen.FindProviderMessageParams{AccountID: string(c.AccountID), ConversationID: string(c.ID), ProviderID: "edit", IsOwn: 0})
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.MessageReference{Scope: "room:stable-authority", Author: "room/bob", ID: "colliding-SID"}
	for _, id := range []domain.ID{root.ID, domain.ID(alias.ID)} {
		row, err := s.queries.GetMessage(t.Context(), string(id))
		if err != nil {
			t.Fatal(err)
		}
		m, err := decodeMessage(row)
		if err != nil {
			t.Fatal(err)
		}
		m.References = []domain.MessageReference{ref}
		if err = recordReplyMetadata(t.Context(), s.queries, string(id), m, false); err != nil {
			t.Fatal(err)
		}
	}
	_ = replySeed(t, s, c, "third distinct canonical", at.Add(2*time.Second), []domain.MessageReference{ref}, nil)
	reply := replySeed(t, s, c, "> fallback\nanswer", at.Add(3*time.Second), nil, &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: 11}}})
	got, err := e.Message(t.Context(), reply.ID)
	if err != nil || got.Reply == nil || got.Reply.Available {
		t.Fatal("alias duplicate rows concealed conflicting target", got, err)
	}
	if _, err = e.Message(t.Context(), migrationID()); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("missing source lost notfound", err)
	}
}

// Reference hydration runs for every visible message during archive replay.
// Check the planner rather than a timing limit so unrelated retained history
// cannot turn each canonical lookup into a full reference scan.
func TestCanonicalReferencesUseIndexedSourceLookups(t *testing.T) {
	s, _, _ := outboxFixture(t)
	source, err := os.ReadFile("queries/corrections.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, query, ok := strings.Cut(string(source), "-- name: ListCanonicalMessageReferences :many")
	if !ok {
		t.Fatal("reference query missing")
	}
	query = strings.ReplaceAll(query, "sqlc.arg(canonical_id)", "?1")
	rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, string(migrationID()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	details := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details += detail + "\n"
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"r", "conflict", "tainted"} {
		if strings.Contains(details, "SCAN "+source+" ") || !strings.Contains(details, "SEARCH "+source+" USING") {
			t.Fatalf("reference source %s must use indexed lookups:\n%s", source, details)
		}
	}
}

func TestConversationEstimateMatchesVisibleLatestPage(t *testing.T) {
	_, e, chat := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	ctx := t.Context()
	query := application.ConversationQuery{Limit: 10, IncludeLatestMessagePageCount: true}
	assertCount := func(want int) {
		t.Helper()
		listed, err := e.Conversations(ctx, "", query)
		if err != nil || len(listed) != 1 || listed[0].LatestMessagePageCount == nil || *listed[0].LatestMessagePageCount != want {
			t.Fatalf("estimated page size, want %d: %+v, %v", want, listed, err)
		}
		messages, err := e.Messages(ctx, chat.ID, "", domain.LatestMessagePageSize)
		if err != nil || min(len(messages), domain.LatestMessagePageSize) != want {
			t.Fatalf("latest page has %d messages, want %d: %v", len(messages), want, err)
		}
	}
	assertCount(0)
	when := time.Now().UTC().Add(time.Hour)
	if _, err := e.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{OriginalText: "hidden until sent"}, &when); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	receiveCorrection(t, e, chat, "original", "First", "", "proof:bob", "bob", at)
	receiveCorrection(t, e, chat, "edit", "Corrected", "original", "proof:bob", "bob", at)
	assertCount(1)
	for range domain.LatestMessagePageSize {
		if _, err := e.SendMessage(ctx, migrationID(), chat.ID, "Visible now"); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(domain.LatestMessagePageSize)
	query.IncludeLatestMessagePageCount = false
	listed, err := e.Conversations(ctx, "", query)
	if err != nil || len(listed) != 1 || listed[0].LatestMessagePageCount != nil {
		t.Fatalf("estimate returned without an opt-in: %+v, %v", listed, err)
	}
}
