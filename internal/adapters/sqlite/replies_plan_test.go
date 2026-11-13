package sqlite

import (
	"os"
	"strings"
	"testing"
)

func TestMessageContextSeeksToTargetTime(t *testing.T) {
	s, _, chat := outboxFixture(t)
	source, err := os.ReadFile("queries/replies.sql")
	if err != nil {
		t.Fatal(err)
	}
	params := strings.NewReplacer(
		"sqlc.arg(conversation_id)", "?1",
		"sqlc.arg(target_ms)", "?2",
		"sqlc.arg(target_id)", "?3",
		"sqlc.arg(page_limit)", "?4",
	)
	for _, name := range []string{"MessagesAtOrBeforeTarget", "MessagesAfterTarget"} {
		t.Run(name, func(t *testing.T) {
			_, query, ok := strings.Cut(string(source), "-- name: "+name+" :many\n")
			if !ok {
				t.Fatal("context query missing")
			}
			query, _, _ = strings.Cut(query, ";")
			rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+params.Replace(query), string(chat.ID), int64(0), string(migrationID()), 9)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if plan := strings.Join(details, "\n"); !strings.Contains(plan, "USING INDEX messages_timeline (conversation_id=? AND <expr>") {
				t.Fatalf("context query must seek by conversation and time:\n%s", plan)
			}
		})
	}
}
