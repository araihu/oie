package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func searchMessages(t *testing.T, e *application.Engine, q domain.MessageSearchQuery) domain.MessageSearchPage {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 50
	}
	p, err := e.SearchMessages(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMessageSearchUsesAccountScopedSenderAvatars(t *testing.T) {
	s, e, direct := outboxFixture(t)
	room := domain.Conversation{ID: migrationID(), RevisionID: migrationID(), AccountID: direct.AccountID, PeerID: migrationID(), Kind: "room", Address: "room@conference.localhost", Title: "Room", UpdatedAt: time.Now().UTC()}
	foreign, err := e.Register(t.Context(), migrationID(), application.RegisterInput{Protocol: "xmpp", Address: "foreign@localhost", Label: "Foreign"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(account domain.ID, address, hash string) {
		t.Helper()
		if err := s.SaveAvatar(t.Context(), domain.Avatar{ID: migrationID(), RevisionID: migrationID(), AccountID: account, Address: address, ImageHash: hash, UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	peerHash, authorHash, ownHash := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	save(direct.AccountID, direct.Address, peerHash)
	save(room.AccountID, "author@localhost", authorHash)
	save(room.AccountID, "alice@localhost", ownHash)
	save(room.AccountID, room.Address, strings.Repeat("d", 64))
	save(foreign.Account.ID, "foreign-peer@localhost", strings.Repeat("e", 64))

	peer := senderSeed(t, s, direct, "")
	known := senderSeed(t, s, room, "author@localhost")
	anonymous := senderSeed(t, s, room, "")
	foreignOnly := senderSeed(t, s, room, "foreign-peer@localhost")
	disputed := senderSeed(t, s, room, "author@localhost")
	disputed.Sender.PersonalAddress = "foreign-peer@localhost"
	if err = s.ReceiveMessage(t.Context(), room, disputed, migrationID()); err != nil {
		t.Fatal(err)
	}
	own := domain.Message{ID: migrationID(), RevisionID: migrationID(), AccountID: direct.AccountID, ConversationID: direct.ID, Sender: domain.Sender{ID: direct.AccountID, DisplayName: "Alice"}, IsOwn: true, OriginalText: "retained source", CreatedAt: time.Now().UTC(), SendState: "received"}
	if err = s.ReceiveMessage(t.Context(), direct, own, migrationID()); err != nil {
		t.Fatal(err)
	}
	page := searchMessages(t, e, domain.MessageSearchQuery{Search: "retained source", AccountID: direct.AccountID})
	want := map[domain.ID]string{peer.ID: peerHash, own.ID: ownHash, known.ID: authorHash, anonymous.ID: "", foreignOnly.ID: "", disputed.ID: ""}
	if len(page.Items) != len(want) {
		t.Fatalf("got %d matches, want %d", len(page.Items), len(want))
	}
	for _, hit := range page.Items {
		hash, found := want[hit.Message.ID]
		if !found || hit.SenderAvatarHash != hash {
			t.Errorf("message %s has avatar %q, want %q", hit.Message.ID, hit.SenderAvatarHash, hash)
		}
		address, sender := room.Address, ""
		switch hit.Message.ID {
		case peer.ID:
			address, sender = direct.Address, direct.Address
		case own.ID:
			address, sender = direct.Address, "alice@localhost"
		}
		if hit.ConversationAddress != address || hit.SenderAddress != sender {
			t.Errorf("message %s has conversation and sender addresses %q, %q, want %q, %q", hit.Message.ID, hit.ConversationAddress, hit.SenderAddress, address, sender)
		}
	}
}

func TestMessageSearchFiltersBeforePagingAndPreservesIngestPosition(t *testing.T) {
	s, e, c := outboxFixture(t)
	base := time.Now().Add(-time.Hour).UTC()
	want := []domain.ID{}
	for i := range 9 {
		m := replySeed(t, s, c, fmt.Sprintf("Cafe\u0301 livre 😀 %d", i), base.Add(time.Duration(i)*time.Second), nil, nil)
		want = append([]domain.ID{m.ID}, want...)
	}
	for i := range 105 {
		replySeed(t, s, c, "unrelated message", base.Add(time.Minute+time.Duration(i)*time.Second), nil, nil)
	}
	q := domain.MessageSearchQuery{Search: " CAFÉ livre 😀 ", AccountID: c.AccountID, ConversationID: c.ID, Limit: 3}
	p := searchMessages(t, e, q)
	if p.Query != "CAFÉ livre 😀" || len(p.Items) != 3 || p.Items[0].Message.ID != want[0] || p.NextCursor == "" {
		t.Fatal(p)
	}
	replySeed(t, s, c, "CAFÉ livre 😀 new", base.Add(time.Hour), nil, nil)
	got := []domain.ID{}
	for {
		for _, h := range p.Items {
			got = append(got, h.Message.ID)
			if h.Historical || h.MatchKind != "text" || h.MatchedRevisionID == "" {
				t.Fatal(h)
			}
		}
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
		p = searchMessages(t, e, q)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatal("keyset lost or repeated existing matches", got, want)
	}
	for _, change := range []func(*domain.MessageSearchQuery){func(q *domain.MessageSearchQuery) { q.Search = "other" }, func(q *domain.MessageSearchQuery) { q.IncludeEditHistory = true }, func(q *domain.MessageSearchQuery) { q.AccountID = "" }, func(q *domain.MessageSearchQuery) { q.ConversationID = "" }} {
		changed := q
		change(&changed)
		if _, err := e.SearchMessages(t.Context(), changed); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("cursor escaped scope", err)
		}
	}
}
func TestMessageSearchReturnsOnlyVerifiedContentHistoryAndCanonicalContext(t *testing.T) {
	s, e, c := outboxFixture(t)
	at := time.Now().Add(-time.Hour).UTC()
	e.SetProtocols(&outboundProtocol{})
	receiveCorrection(t, e, c, "first", "old signal", "", "proof:bob", "bob", at)
	original := correctionMessages(t, e, c)[0]
	receiveCorrection(t, e, c, "second", "middle signal", "first", "proof:bob", "bob", at.Add(time.Second))
	receiveCorrection(t, e, c, "third", "current signal", "second", "proof:bob", "bob", at.Add(2*time.Second))
	q := domain.MessageSearchQuery{Search: "signal", Limit: 1}
	p := searchMessages(t, e, q)
	if len(p.Items) != 1 || p.Items[0].Message.ID != original.ID || p.Items[0].Excerpt != "current signal" || p.Items[0].Historical || p.NextCursor != "" {
		t.Fatal(p)
	}
	q.IncludeEditHistory = true
	hits := []domain.MessageSearchHit{}
	for {
		p = searchMessages(t, e, q)
		hits = append(hits, p.Items...)
		if p.NextCursor == "" {
			break
		}
		q.Cursor = p.NextCursor
	}
	if len(hits) != 3 || hits[0].Historical || !hits[1].Historical || !hits[2].Historical || hits[1].Excerpt != "middle signal" || hits[2].Excerpt != "old signal" {
		t.Fatal(hits)
	}
	seen := map[domain.ID]bool{}
	for _, h := range hits {
		if h.Message.ID != original.ID || h.Message.OriginalText != "current signal" || seen[h.MatchedRevisionID] {
			t.Fatal(h)
		}
		seen[h.MatchedRevisionID] = true
	}
	if p := searchMessages(t, e, domain.MessageSearchQuery{Search: "old", Limit: 10}); len(p.Items) != 0 {
		t.Fatal("old text searched by default", p)
	}
	// Ordinary outbox delivery revisions must not manufacture edit history.
	sent, err := e.SendMessage(t.Context(), migrationID(), c.ID, "transport needle")
	if err != nil {
		t.Fatal(err)
	}
	initial := searchMessages(t, e, domain.MessageSearchQuery{Search: "transport needle", IncludeEditHistory: true, Limit: 10})
	if _, err = s.TransitionMessage(t.Context(), sent.Message.ID, "queued", "dispatching", migrationID(), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	after := searchMessages(t, e, domain.MessageSearchQuery{Search: "transport needle", IncludeEditHistory: true, Limit: 10})
	if len(after.Items) != 1 || after.Items[0].MatchedRevisionID != initial.Items[0].MatchedRevisionID || after.Items[0].Message.SendState != "dispatching" {
		t.Fatal(after)
	}
}
func TestMessageSearchCurrentMetadataIsLocalAndInvalidatesOnCorrection(t *testing.T) {
	s, e, c := outboxFixture(t)
	at := time.Now().Add(-time.Hour).UTC()
	e.SetProtocols(&outboundProtocol{})
	receiveCorrection(t, e, c, "first", "source https://example.test/first.pdf", "", "proof:bob", "bob", at)
	m := correctionMessages(t, e, c)[0]
	refs := application.SharedReferences(m.OriginalText)
	for i := range refs {
		refs[i].ID = migrationID()
		refs[i].RevisionID = migrationID()
	}
	if err := s.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: m, References: refs, ParserVersion: application.SharedContentVersion}}); err != nil {
		t.Fatal(err)
	}
	preview := domain.MessagePreview{ID: migrationID(), RevisionID: migrationID(), MessageID: m.ID, AccountID: c.AccountID, ConversationID: c.ID, URL: refs[0].URL, Title: "Orçamento especial", Description: "annual projection", State: "ready", Kind: "link", DownloadState: "not_downloaded", ImageHash: strings.Repeat("a", 64), UpdatedAt: time.Now().UTC()}
	if _, err := s.EnsurePreview(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	before, err := e.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page := searchMessages(t, e, domain.MessageSearchQuery{Search: "orcamento", Limit: 10})
	if len(page.Items) != 1 || page.Items[0].MatchKind != "metadata" || !strings.Contains(page.Items[0].Excerpt, "Orçamento especial") || page.MetadataIndexing.State != "complete" {
		t.Fatal(page)
	}
	if p := page.Items[0].Preview; p == nil || p.Kind != "document" || p.ImageHash != preview.ImageHash {
		t.Fatalf("missing cached preview: %+v", p)
	}
	after, _ := e.Snapshot(t.Context())
	if before.Sequence != after.Sequence {
		t.Fatal("search created events")
	}
	p, _ := s.Preview(t.Context(), preview.ID)
	if p.RevisionID != preview.RevisionID || p.State != "ready" {
		t.Fatal("search queued enrichment")
	}
	receiveCorrection(t, e, c, "edit", "new body https://example.test/next.pdf", "first", "proof:bob", "bob", at.Add(time.Second))
	stale := searchMessages(t, e, domain.MessageSearchQuery{Search: "orcamento", IncludeEditHistory: true, Limit: 10})
	if len(stale.Items) != 0 || stale.MetadataIndexing.State != "pending" {
		t.Fatal("stale metadata leaked", stale)
	}
	// Immediate current text does not wait for the metadata worker.
	current := searchMessages(t, e, domain.MessageSearchQuery{Search: "next.pdf", Limit: 10})
	if len(current.Items) != 1 || current.Items[0].Historical || current.Items[0].Preview != nil {
		t.Fatal(current)
	}
	history := searchMessages(t, e, domain.MessageSearchQuery{Search: "first.pdf", IncludeEditHistory: true, Limit: 10})
	if len(history.Items) != 1 || !history.Items[0].Historical || history.Items[0].Preview != nil {
		t.Fatal(history)
	}
}
func TestMessageSearchValidationAndPlainBoundedExcerpts(t *testing.T) {
	s, e, c := outboxFixture(t)
	for _, q := range []domain.MessageSearchQuery{{Search: " "}, {Search: "\x00"}, {Search: string([]byte{255})}, {Search: strings.Repeat("😀", 201)}, {Search: "okay", Limit: 101}, {Search: "okay", AccountID: "notUUID"}, {Search: "okay", Cursor: "!!"}} {
		if q.Limit == 0 {
			q.Limit = 10
		}
		if _, err := e.SearchMessages(t.Context(), q); !errors.Is(err, application.ErrInvalid) {
			t.Fatal(q, err)
		}
	}
	body := strings.Repeat("before ", 120) + "<script>alert('needle')</script> 😀" + strings.Repeat(" after", 100)
	replySeed(t, s, c, body, time.Now().UTC(), nil, nil)
	p := searchMessages(t, e, domain.MessageSearchQuery{Search: "needle", Limit: 10})
	if len(p.Items) != 1 || utf8.RuneCountInString(p.Items[0].Excerpt) > 512 || !strings.Contains(p.Items[0].Excerpt, "<script>") {
		t.Fatal(p)
	}
	for _, q := range []string{"%_", "' OR 1=1 --", "\""} {
		p = searchMessages(t, e, domain.MessageSearchQuery{Search: q, Limit: 10})
		if len(p.Items) != 0 {
			t.Fatal("query interpreted as operators", q, p)
		}
	}
}
func TestMessageSearchConcurrentIngestAndReads(t *testing.T) {
	s, e, c := outboxFixture(t)
	at := time.Now().Add(-time.Hour).UTC()
	e.SetProtocols(&outboundProtocol{})
	receiveCorrection(t, e, c, "original", "needle original", "", "proof:bob", "bob", at)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Go(func() {
		for i := range 12 {
			err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: fmt.Sprint("update", i), OriginalText: fmt.Sprint("needle revision ", i), SenderName: "Bob", Kind: c.Kind, Historical: true, CreatedAt: at.Add(time.Duration(i+1) * time.Second), Correction: &domain.MessageCorrection{Scope: "proof:bob", Author: "bob", ID: fmt.Sprint("update", i), TargetID: "original"}})
			if err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Go(func() {
		for range 20 {
			p, err := e.SearchMessages(t.Context(), domain.MessageSearchQuery{Search: "needle", IncludeEditHistory: true, Limit: 100})
			if err != nil {
				errs <- err
				return
			}
			for _, h := range p.Items {
				if !h.Historical && h.Excerpt != h.Message.OriginalText {
					errs <- fmt.Errorf("current content not in one snapshot: %v", h)
					return
				}
			}
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	_ = s
}

func TestMessageSearchOrdersMillisecondTimestampTiesByCanonicalUUID(t *testing.T) {
	s, e, c := outboxFixture(t)
	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	higherTime := domain.Message{ID: domain.ID("019a5df0-3333-7000-8000-000000000001"), RevisionID: migrationID(), AccountID: c.AccountID, ConversationID: c.ID, OriginalText: "timestamp precision", CreatedAt: base.Add(900 * time.Microsecond), SendState: "received", ProviderID: "precision-one"}
	higherID := higherTime
	higherID.ID = domain.ID("019a5df0-3333-7000-8000-000000000002")
	higherID.RevisionID = migrationID()
	higherID.CreatedAt = base.Add(100 * time.Microsecond)
	higherID.ProviderID = "precision-two"
	for _, m := range []domain.Message{higherTime, higherID} {
		if err := s.ReceiveMessage(t.Context(), c, m, migrationID()); err != nil {
			t.Fatal(err)
		}
	}
	q := domain.MessageSearchQuery{Search: "timestamp precision", Limit: 1}
	first := searchMessages(t, e, q)
	if len(first.Items) != 1 || first.Items[0].Message.ID != higherID.ID || first.NextCursor == "" {
		t.Fatal(first)
	}
	q.Cursor = first.NextCursor
	second := searchMessages(t, e, q)
	if len(second.Items) != 1 || second.Items[0].Message.ID != higherTime.ID || second.NextCursor != "" {
		t.Fatal(second)
	}
}
