package sqlite

import (
	"fmt"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

func TestFuzzyCatalogSearchFiltersBeforePagination(t *testing.T) {
	s, engine, conversation := outboxFixture(t)
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for i := range 30 {
		replySeed(t, s, conversation, fmt.Sprintf("Unrelated newer history https://files.example.test/other-%d.pdf", i), at.Add(time.Duration(i)*time.Minute), nil, nil)
	}
	for i := range 8 {
		replySeed(t, s, conversation, fmt.Sprintf("Relatório de orçamento e faturamento https://files.example.test/report-%d.pdf", i), at.Add(-time.Duration(i+1)*time.Hour), nil, nil)
	}
	other := catalogConversation(t, s, conversation.AccountID, "other@localhost", "Other chat")
	replySeed(t, s, other, "Relatório de orçamento e faturamento https://files.example.test/private.pdf", at, nil, nil)
	stop := startCatalog(t, engine)
	defer stop()
	waitCatalog(t, s, 39)
	seen := map[domain.ID]bool{}
	cursor := ""
	for {
		page, err := engine.SharedContent(t.Context(), domain.SharedContentQuery{Type: "documents", AccountID: conversation.AccountID, ConversationID: conversation.ID, Search: "orcamento faturamneto", Cursor: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.Message.ConversationID != conversation.ID || seen[item.ID] {
				t.Fatal("fuzzy search leaked scope or repeated a cursor item", item)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 8 {
		t.Fatalf("matches from older pages were omitted: %d", len(seen))
	}
}
