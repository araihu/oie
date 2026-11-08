package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestCorrectionAliasesPreserveStarsAndReplaceSharedCatalog(t *testing.T) {
	s, e, c := outboxFixture(t)
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	original := replySeed(t, s, c, "First https://example.test/old.pdf", at, nil, nil)
	correction := replySeed(t, s, c, "Corrected https://example.test/new.pdf", at.Add(time.Second), nil, nil)
	star, err := e.SetMessageStar(t.Context(), migrationID(), correction.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	stop := startCatalog(t, e)
	defer stop()
	waitCatalog(t, s, 2)
	before, err := s.SharedContent(t.Context(), domain.SharedContentQuery{Type: "documents", Limit: 10})
	if err != nil || len(before.Items) != 2 {
		t.Fatal("legacy catalog fixture", err)
	}
	original.Correction = &domain.MessageCorrection{Scope: "sender:verified", Author: "verified", ID: original.ProviderID}
	correction.Correction = &domain.MessageCorrection{Scope: original.Correction.Scope, Author: original.Correction.Author, ID: correction.ProviderID, TargetID: original.ProviderID}
	for _, source := range []domain.Message{correction, original} {
		if err := s.ReceiveMessage(t.Context(), c, source, migrationID()); err != nil {
			t.Fatal(err)
		}
	}
	waitCatalog(t, s, 1)
	page, err := s.SharedContent(t.Context(), domain.SharedContentQuery{Type: "documents", Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].Message.ID != original.ID || page.Items[0].Message.OriginalText != correction.OriginalText || page.Items[0].URL != "https://example.test/new.pdf" {
		t.Fatal("catalog retained old content or a correction alias", page, err)
	}
	old, err := s.SharedContent(t.Context(), domain.SharedContentQuery{Type: "documents", Search: "old.pdf", Limit: 10})
	if err != nil || len(old.Items) != 0 {
		t.Fatal("superseded URL still searchable", err)
	}
	for _, sourceID := range []domain.ID{original.ID, correction.ID} {
		state, err := e.MessageStar(t.Context(), sourceID)
		if err != nil || !state.Starred || state.MessageID != original.ID || state.RevisionID != star.Star.RevisionID {
			t.Fatal("source star lost across canonical alias", state, err)
		}
	}
	starred, err := e.StarredMessages(t.Context(), application.StarQuery{ConversationID: c.ID, Limit: 10})
	if err != nil || len(starred) != 1 || starred[0].Message.ID != original.ID || starred[0].Message.OriginalText != correction.OriginalText || starred[0].Star.MessageID != original.ID {
		t.Fatal("starred list did not project one canonical current message", starred, err)
	}
	states, err := e.MessageStars(t.Context(), c.ID, []domain.ID{original.ID, correction.ID})
	if err != nil || len(states) != 1 || states[0].MessageID != original.ID {
		t.Fatal("star batch duplicated aliases", states, err)
	}
	unstarred, err := e.SetMessageStar(t.Context(), migrationID(), original.ID, star.Star.RevisionID, false)
	if err != nil || unstarred.Star.Starred {
		t.Fatal("unstar canonical source", err)
	}
	starred, err = e.StarredMessages(t.Context(), application.StarQuery{Limit: 10})
	if err != nil || len(starred) != 0 {
		t.Fatal("older alias star resurrected after unstar", err)
	}
	if _, err = e.SetMessageStar(t.Context(), migrationID(), correction.ID, star.Star.RevisionID, true); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("alias bypassed star revision precondition", err)
	}
	for _, source := range []domain.Message{original, correction} {
		var body string
		if err := s.db.QueryRow("SELECT json_extract(payload,'$.OriginalText') FROM messages WHERE id=?", source.ID).Scan(&body); err != nil || body != source.OriginalText {
			t.Fatal("correction changed a retained source body", err)
		}
	}
	var rows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM message_star_revisions").Scan(&rows); err != nil || rows != 2 {
		t.Fatal("star history was rewritten", rows, err)
	}
}
