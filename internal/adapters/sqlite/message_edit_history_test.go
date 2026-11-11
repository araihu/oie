package sqlite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func editHistory(t *testing.T, e *application.Engine, id domain.ID, cursor string, limit int) domain.MessageEditHistoryPage {
	t.Helper()
	page, err := e.MessageEditHistory(t.Context(), id, cursor, limit)
	if err != nil {
		t.Fatal(err)
	}
	return page
}
func historySource(t *testing.T, s *Store, c domain.Conversation, provider string) domain.ID {
	t.Helper()
	row, err := s.queries.FindProviderMessage(t.Context(), dbgen.FindProviderMessageParams{AccountID: string(c.AccountID), ConversationID: string(c.ID), ProviderID: provider, IsOwn: 0})
	if err != nil {
		t.Fatal(err)
	}
	return domain.ID(row.ID)
}
func TestEditHistoryPagesVerifiedContentAndCanonicalAliases(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().Add(-time.Hour).UTC()
	receiveCorrection(t, e, c, "first", "original **source** 😀", "", "proof:bob", "bob", at)
	root := correctionMessages(t, e, c)[0]
	receiveCorrection(t, e, c, "second", "middle source", "first", "proof:bob", "bob", at.Add(time.Second))
	receiveCorrection(t, e, c, "third", "latest source", "second", "proof:bob", "bob", at.Add(2*time.Second))
	alias := historySource(t, s, c, "third")
	_, before, _ := s.Snapshot(t.Context())
	first := editHistory(t, e, root.ID, "", 1)
	if first.AccountID != c.AccountID || first.ConversationID != c.ID || first.MessageID != root.ID || first.RequestedMessageID != root.ID || len(first.Snapshot) != 64 || first.NextCursor == "" || len(first.Items) != 1 || first.Items[0].OriginalText != "latest source" || !first.Items[0].IsCurrent || first.Items[0].IsOriginal || first.CurrentRevisionID != first.Items[0].RevisionID {
		t.Fatal(first)
	}
	second := editHistory(t, e, alias, first.NextCursor, 1)
	if second.MessageID != root.ID || second.RequestedMessageID != alias || second.Snapshot != first.Snapshot || len(second.Items) != 1 || second.Items[0].OriginalText != "middle source" || second.Items[0].IsCurrent || second.Items[0].IsOriginal || second.NextCursor == "" {
		t.Fatal(second)
	}
	third := editHistory(t, e, root.ID, second.NextCursor, 1)
	if len(third.Items) != 1 || third.Items[0].OriginalText != "original **source** 😀" || !third.Items[0].IsOriginal || third.Items[0].IsCurrent || third.NextCursor != "" || third.Items[0].RevisionID != root.RevisionID || len(third.Items[0].ParentRevisionIDs) != 0 {
		t.Fatal(third)
	}
	if !reflect.DeepEqual(first.Items[0].ParentRevisionIDs, []domain.ID{second.Items[0].RevisionID}) || !reflect.DeepEqual(second.Items[0].ParentRevisionIDs, []domain.ID{third.Items[0].RevisionID}) {
		t.Fatal("lost observed content ancestry")
	}
	_, after, _ := s.Snapshot(t.Context())
	if before != after {
		t.Fatal("history read mutated durable event log")
	}
	// Unrelated arrival does not invalidate this content snapshot.
	replySeed(t, s, c, "unrelated arrival", at.Add(time.Hour), nil, nil)
	again := editHistory(t, e, root.ID, first.NextCursor, 1)
	if again.Snapshot != first.Snapshot || again.Items[0].RevisionID != second.Items[0].RevisionID {
		t.Fatal("unrelated activity invalidated history")
	}
	receiveCorrection(t, e, c, "fourth", "new latest source", "third", "proof:bob", "bob", at.Add(3*time.Second))
	if _, err := e.MessageEditHistory(t.Context(), root.ID, first.NextCursor, 1); !errors.Is(err, application.ErrHistoryChanged) {
		t.Fatal("stale content snapshot accepted", err)
	}
	fresh := editHistory(t, e, root.ID, "", 20)
	if len(fresh.Items) != 4 || fresh.Items[0].OriginalText != "new latest source" || fresh.Snapshot == first.Snapshot {
		t.Fatal(fresh)
	}
}
func TestEditHistoryNeverPromotesTransportTransitionsToContent(t *testing.T) {
	s, e, c := outboxFixture(t)
	accepted, err := e.SendMessage(t.Context(), migrationID(), c.ID, "immutable authored body")
	if err != nil {
		t.Fatal(err)
	}
	before := editHistory(t, e, accepted.Message.ID, "", 20)
	if len(before.Items) != 1 || before.CurrentRevisionID != accepted.Message.RevisionID || !before.Items[0].IsCurrent || !before.Items[0].IsOriginal {
		t.Fatal(before)
	}
	for _, transition := range [][2]string{{"queued", "dispatching"}, {"dispatching", "submitted"}} {
		if _, err = s.TransitionMessage(t.Context(), accepted.Message.ID, transition[0], transition[1], migrationID(), time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	after := editHistory(t, e, accepted.Message.ID, "", 20)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("delivery evidence changed content history", before, after)
	}
}
func TestEditHistoryCurrentDoesNotDependOnDisplayOrderOrInventMissingOriginal(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().UTC()
	receiveCorrection(t, e, c, "orphan", "first observed correction", "not-yet-observed", "proof:bob", "bob", at)
	orphan := correctionMessages(t, e, c)[0]
	initial := editHistory(t, e, orphan.ID, "", 20)
	if len(initial.Items) != 1 || initial.Items[0].IsOriginal || !initial.Items[0].IsCurrent || len(initial.Items[0].ParentRevisionIDs) != 0 {
		t.Fatal("orphan presented as an observed original", initial)
	}
	receiveCorrection(t, e, c, "not-yet-observed", "late imported original", "", "proof:bob", "bob", at.Add(time.Hour))
	resolved := editHistory(t, e, orphan.ID, "", 20)
	if len(resolved.Items) != 2 || !resolved.Items[0].IsOriginal || resolved.Items[0].IsCurrent || !resolved.Items[1].IsCurrent || resolved.Items[1].IsOriginal || resolved.CurrentRevisionID != resolved.Items[1].RevisionID {
		t.Fatal("timestamp replaced causal current or lost original", resolved)
	}
	if resolved.RequestedMessageID != orphan.ID || resolved.MessageID == orphan.ID || resolved.Items[0].SourceMessageID != resolved.MessageID {
		t.Fatal("late original lost canonical alias", resolved)
	}
	if len(resolved.Items[1].ParentRevisionIDs) != 1 || resolved.Items[1].ParentRevisionIDs[0] != resolved.Items[0].RevisionID {
		t.Fatal("missing observed parent after late import")
	}
	_ = s
}
func TestEditHistoryRejectsForeignMalformedAndOversizedCursors(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().UTC()
	for i := range 3 {
		target := ""
		if i > 0 {
			target = fmt.Sprint(i - 1)
		}
		receiveCorrection(t, e, c, fmt.Sprint(i), fmt.Sprint("body", i), target, "proof:bob", "bob", at.Add(time.Duration(i)*time.Second))
	}
	root := correctionMessages(t, e, c)[0]
	page := editHistory(t, e, root.ID, "", 1)
	other := replySeed(t, s, c, "other message", at, nil, nil)
	if _, err := e.MessageEditHistory(t.Context(), other.ID, page.NextCursor, 1); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("cursor escaped canonical message", err)
	}
	for _, cursor := range []string{"!", base64.RawURLEncoding.EncodeToString([]byte(`{"unknown":1}`)), base64.RawURLEncoding.EncodeToString([]byte(`{} {}`)), string(make([]byte, 2049))} {
		if _, err := e.MessageEditHistory(t.Context(), root.ID, cursor, 1); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("malformed cursor accepted", err)
		}
	}
	var cursorValue map[string]any
	raw, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cursorValue); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		field string
		value any
		want  error
	}{
		{"revision", string(migrationID()), application.ErrInvalid},
		{"at", at.Add(24 * time.Hour).Format(time.RFC3339Nano), application.ErrInvalid},
		{"scope", strings.Repeat("a", 64), application.ErrInvalid},
		{"snapshot", strings.Repeat("a", 64), application.ErrHistoryChanged},
		{"unexpected", true, application.ErrInvalid},
	} {
		changed := map[string]any{}
		for k, v := range cursorValue {
			changed[k] = v
		}
		changed[mutation.field] = mutation.value
		data, _ := json.Marshal(changed)
		if _, err = e.MessageEditHistory(t.Context(), root.ID, base64.RawURLEncoding.EncodeToString(data), 1); !errors.Is(err, mutation.want) {
			t.Fatalf("%s cursor alteration: %v", mutation.field, err)
		}
	}
	otherEngine, err := application.New(historyEngineIdentity{Store: s, engineID: migrationID()}, "local", func() (domain.ID, error) { return migrationID(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = otherEngine.MessageEditHistory(t.Context(), root.ID, page.NextCursor, 1); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("history cursor crossed engine identity", err)
	}
	for _, limit := range []int{0, 51} {
		if _, err := e.MessageEditHistory(t.Context(), root.ID, "", limit); !errors.Is(err, application.ErrInvalid) {
			t.Fatal("unbounded limit accepted", err)
		}
	}
	if _, err := e.MessageEditHistory(t.Context(), migrationID(), "", 20); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("missing source accepted", err)
	}
	if _, err := e.MessageEditHistory(t.Context(), "bad", "", 20); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("invalid source accepted", err)
	}
}
func TestEditHistoryExcludesConflictingAndForeignCorrectionRelationships(t *testing.T) {
	s, e, c := outboxFixture(t)
	e.SetProtocols(&outboundProtocol{})
	at := time.Now().UTC()
	receiveCorrection(t, e, c, "first", "original", "", "proof:bob", "bob", at)
	root := correctionMessages(t, e, c)[0]
	receiveCorrection(t, e, c, "second", "edited", "first", "proof:bob", "bob", at.Add(time.Second))
	receiveCorrection(t, e, c, "foreign", "other author cannot edit", "first", "proof:bob", "other-author", at.Add(2*time.Second))
	page := editHistory(t, e, root.ID, "", 20)
	if len(page.Items) != 2 {
		t.Fatal("foreign author entered source history", page)
	}
	receiveCorrection(t, e, c, "first", "colliding source body", "", "proof:bob", "bob", at)
	page = editHistory(t, e, root.ID, "", 20)
	if len(page.Items) != 1 || page.Items[0].OriginalText != "original" || page.Items[0].IsOriginal {
		t.Fatal("ambiguous metadata remained history or original proof", page)
	}
	_ = s
}

func TestEditHistoryMigration13To14PreservesSourceIdentityAndPendingSends(t *testing.T) {
	s, e, c := outboxFixture(t)
	source := senderSeed(t, s, c, "author@example.test")
	pending, err := e.SendMessage(t.Context(), migrationID(), c.ID, "retain queued send")
	if err != nil {
		t.Fatal(err)
	}
	engine, sequence, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var sourcePayload string
	if err = s.db.QueryRow("SELECT payload FROM messages WHERE id=?", source.ID).Scan(&sourcePayload); err != nil {
		t.Fatal(err)
	}
	editDown, err := migrations.ReadFile("migrations/000015_message_edit_commands.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(string(editDown)); err != nil {
		t.Fatal(err)
	}
	downReactionMigration(t, s)
	down, err := migrations.ReadFile("migrations/000014_message_revision_index.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(string(down)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE schema_migrations SET version=13,dirty=0"); err != nil {
		t.Fatal(err)
	}
	var databaseIndex int
	var databaseName, path string
	if err = s.db.QueryRow("PRAGMA database_list").Scan(&databaseIndex, &databaseName, &path); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), filepath.Dir(path), migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = upgraded.Close() }()
	got, err := upgraded.Message(t.Context(), source.ID)
	if err != nil || got.Sender.PersonalAddress != "author@example.test" || got.RevisionID != source.RevisionID {
		t.Fatal("migration changed source author", got, err)
	}
	var afterPayload string
	if err = upgraded.db.QueryRow("SELECT payload FROM messages WHERE id=?", source.ID).Scan(&afterPayload); err != nil || sourcePayload != afterPayload {
		t.Fatal("migration rewrote source")
	}
	afterEngine, afterSequence, err := upgraded.Snapshot(t.Context())
	if err != nil || afterEngine != engine || afterSequence != sequence {
		t.Fatal("migration changed durable identity/events", err)
	}
	queued, err := upgraded.Message(t.Context(), pending.Message.ID)
	if err != nil || queued.SendState != "queued" || queued.RevisionID != pending.Message.RevisionID || queued.OriginalText != pending.Message.OriginalText {
		t.Fatal("migration changed pending work", queued, err)
	}
	var version, indexes int
	var dirty bool
	if err = upgraded.db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != latestMigration || dirty {
		t.Fatal("unclean migration", err)
	}
	if err = upgraded.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='message_revisions_entity'").Scan(&indexes); err != nil || indexes != 1 {
		t.Fatal("history index missing", err)
	}
}

func TestEditHistoryQueryPlansRemainBoundedWithLargeUnrelatedRevisionHistory(t *testing.T) {
	s, e, c := outboxFixture(t)
	at := time.Now().UTC()
	source := replySeed(t, s, c, "selected source", at, nil, nil)
	unrelated := replySeed(t, s, c, "unrelated transport history", at.Add(time.Second), nil, nil)
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.Prepare("INSERT INTO message_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?)")
	if err != nil {
		t.Fatal(err)
	}
	for range 6000 {
		if _, err = statement.Exec(string(migrationID()), string(unrelated.ID), "[]", jsonText(unrelated), stamp(at)); err != nil {
			t.Fatal(err)
		}
	}
	if err = statement.Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("queries/message_edit_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[string]string{"canonical_id": string(source.ID), "account_id": string(c.AccountID), "conversation_id": string(c.ID)}
	macros := regexp.MustCompile(`sqlc\.arg\(([a-z_]+)\)`)
	for _, part := range strings.Split(string(data), "-- name: ")[1:] {
		line, query, ok := strings.Cut(part, "\n")
		if !ok {
			t.Fatal("missing query")
		}
		args := []any{}
		query = macros.ReplaceAllStringFunc(query, func(match string) string {
			name := macros.FindStringSubmatch(match)[1]
			args = append(args, scopes[name])
			return "?"
		})
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		details := []string{}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
		plan := strings.Join(details, "\n")
		for _, table := range []string{"first", "m", "f", "r"} {
			if regexp.MustCompile(`\bSCAN ` + table + `\b`).MatchString(plan) {
				t.Fatalf("%s scans unbounded retained data:\n%s", line, plan)
			}
		}
		if strings.Contains(line, "Sources") && !strings.Contains(plan, "entity_id=?") {
			t.Fatalf("first-source lookup lacks an entity seek:\n%s", plan)
		}
	}
	page := editHistory(t, e, source.ID, "", 20)
	if len(page.Items) != 1 || page.Items[0].OriginalText != "selected source" {
		t.Fatal("unrelated revisions entered history", page)
	}
}

// The same retained IDs under another engine must not accept a saved cursor.
type historyEngineIdentity struct {
	*Store
	engineID domain.ID
}

func (s historyEngineIdentity) Snapshot(ctx context.Context) (domain.ID, int64, error) {
	_, sequence, err := s.Store.Snapshot(ctx)
	return s.engineID, sequence, err
}
