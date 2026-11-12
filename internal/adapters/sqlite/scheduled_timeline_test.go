package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestScheduledMessagesStayOutOfTimelineSearchAndCatalog(t *testing.T) {
	s, engine, chat := outboxFixture(t)
	ctx := t.Context()
	older := replySeed(t, s, chat, "older neighbor", time.Now().UTC().Add(-time.Minute), nil, nil)
	when := time.Now().UTC().Add(time.Hour)
	const address = "https://example.test/unsent"
	pending, err := engine.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{OriginalText: "private needle " + address}, &when)
	if err != nil {
		t.Fatal(err)
	}
	newer := replySeed(t, s, chat, "newer neighbor", when.Add(-time.Minute), nil, nil)
	assertHidden := func() {
		t.Helper()
		items, err := s.Messages(ctx, chat.ID, "", 10)
		if err != nil || len(items) != 2 || items[0].ID != newer.ID || items[1].ID != older.ID {
			t.Fatalf("timeline included an unpublished message: %+v %v", items, err)
		}
		items, err = s.MessagesAfter(ctx, chat.ID, older.ID, 10)
		if err != nil || len(items) != 1 || items[0].ID != newer.ID {
			t.Fatalf("newer page included an unpublished message: %+v %v", items, err)
		}
		if _, err = s.MessageContext(ctx, chat.ID, pending.Message.ID, 10); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("unpublished context target returned %v", err)
		}
		for _, target := range []domain.ID{older.ID, newer.ID} {
			window, err := s.MessageContext(ctx, chat.ID, target, 10)
			if err != nil || len(window.Items) != 2 || window.Items[0].ID != newer.ID || window.Items[1].ID != older.ID {
				t.Fatalf("context included an unpublished message: %+v %v", window, err)
			}
		}
		if hits := searchMessages(t, engine, domain.MessageSearchQuery{Search: "private needle"}); len(hits.Items) != 0 || hits.MetadataIndexing.TotalMessages != 2 {
			t.Fatalf("search included an unpublished message: %+v", hits)
		}
		links, err := s.SharedContent(ctx, domain.SharedContentQuery{Type: "links", Limit: 10})
		if err != nil || len(links.Items) != 0 || links.Indexing.TotalMessages != 2 {
			t.Fatalf("catalog included an unpublished message: %+v %v", links, err)
		}
	}
	assertHidden()
	candidates, err := s.SharedContentCandidates(ctx, application.SharedContentVersion, 10)
	if err != nil || len(candidates) != 2 || candidates[0].ID != newer.ID || candidates[1].ID != older.ID {
		t.Fatalf("indexer included an unpublished message: %+v %v", candidates, err)
	}
	// Simulate a catalog entry retained from a database that indexed schedules.
	ref := migrationID()
	if err = s.queries.PutSharedContent(ctx, dbgen.PutSharedContentParams{ID: string(ref), RevisionID: string(migrationID()), MessageID: string(pending.Message.ID), Url: address, Kind: "link"}); err != nil {
		t.Fatal(err)
	}
	if err = s.queries.MarkSharedContentIndexed(ctx, dbgen.MarkSharedContentIndexedParams{MessageID: string(pending.Message.ID), ParserVersion: application.SharedContentVersion}); err != nil {
		t.Fatal(err)
	}
	assertHidden()
	if _, err = s.SharedContentItem(ctx, ref); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("unpublished catalog item returned %v", err)
	}
	if _, err = engine.CancelMessage(ctx, migrationID(), pending.Message.ID, pending.Message.RevisionID); err != nil {
		t.Fatal(err)
	}
	assertHidden()
	message, err := engine.Message(ctx, pending.Message.ID)
	if err != nil || message.SendState != "cancelled" || message.ScheduledAt == nil {
		t.Fatalf("cancellation evidence no longer addressable: %+v %v", message, err)
	}
}

func TestReleasedScheduleUsesStableTimelineAndCatalogPosition(t *testing.T) {
	for _, method := range []string{"automatic", "send_now"} {
		t.Run(method, func(t *testing.T) {
			s, _, chat := outboxFixture(t)
			ctx := t.Context()
			clock := time.Now().UTC().Truncate(time.Millisecond).Add(600 * time.Microsecond)
			engine, err := application.New(s, "local", func() (domain.ID, error) { return migrationID(), nil }, func() time.Time { return clock })
			if err != nil {
				t.Fatal(err)
			}
			createdAt, due := clock, clock.Add(3*time.Hour)
			pending, err := engine.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{OriginalText: "position https://example.test/scheduled"}, &due)
			if err != nil {
				t.Fatal(err)
			}
			neighbor := replySeed(t, s, chat, "position https://example.test/intervening", createdAt.Add(time.Hour), nil, nil)
			clock = due
			if method == "send_now" {
				clock = due.Add(-30 * time.Minute)
				firstRelease, err := engine.UpdatePendingMessage(ctx, migrationID(), pending.Message.ID, pending.Message.RevisionID, nil, nil, true)
				if err != nil {
					t.Fatal(err)
				}
				rescheduled, err := engine.UpdatePendingMessage(ctx, migrationID(), pending.Message.ID, firstRelease.Message.RevisionID, nil, &due, false)
				if err != nil {
					t.Fatal(err)
				}
				// An engine clock adjustment must not revive the earlier release
				// with the later wall-clock timestamp during migration backfill.
				clock = due.Add(-time.Hour)
				released, err := engine.UpdatePendingMessage(ctx, migrationID(), pending.Message.ID, rescheduled.Message.RevisionID, nil, nil, true)
				if err != nil || released.Message.TimelineAt == nil || !released.Message.TimelineAt.Equal(clock.Truncate(time.Millisecond)) || released.Message.ScheduledAt != nil {
					t.Fatalf("send-now release did not enter the timeline: %+v %v", released, err)
				}
			}
			position := clock
			if _, err = s.TransitionMessage(ctx, pending.Message.ID, "queued", "dispatching", migrationID(), clock, clock); err != nil {
				t.Fatal(err)
			}
			if method == "automatic" {
				if _, err = s.MessageContext(ctx, chat.ID, pending.Message.ID, 5); !errors.Is(err, application.ErrNotFound) {
					t.Fatalf("automatic dispatch became visible before its result: %v", err)
				}
				position = position.Add(time.Second)
			}
			position = position.Truncate(time.Millisecond)
			clock = clock.Add(time.Second)
			if _, err = s.TransitionMessage(ctx, pending.Message.ID, "dispatching", "submitted", migrationID(), clock, clock); err != nil {
				t.Fatal(err)
			}
			latest, err := s.Messages(ctx, chat.ID, "", 1)
			if err != nil || len(latest) != 1 || latest[0].ID != pending.Message.ID || latest[0].TimelineAt == nil || !latest[0].TimelineAt.Equal(position) || !latest[0].CreatedAt.Equal(createdAt) {
				t.Fatalf("released message lost position or creation time: %+v %v", latest, err)
			}
			older, err := s.Messages(ctx, chat.ID, pending.Message.ID, 1)
			if err != nil || len(older) != 1 || older[0].ID != neighbor.ID {
				t.Fatalf("older keyset skipped the neighbor: %+v %v", older, err)
			}
			newer, err := s.MessagesAfter(ctx, chat.ID, neighbor.ID, 1)
			if err != nil || len(newer) != 1 || newer[0].ID != pending.Message.ID {
				t.Fatalf("newer keyset skipped the release: %+v %v", newer, err)
			}
			window, err := s.MessageContext(ctx, chat.ID, neighbor.ID, 3)
			if err != nil || len(window.Items) != 2 || window.Items[0].ID != pending.Message.ID {
				t.Fatalf("context disagreed with timeline order: %+v %v", window, err)
			}
			conversation, err := engine.Conversation(ctx, chat.ID)
			if err != nil || conversation.LastMessage != pending.Message.OriginalText || !conversation.UpdatedAt.Equal(position) {
				t.Fatalf("sidebar disagreed with timeline: %+v %v", conversation, err)
			}
			search := domain.MessageSearchQuery{Search: "position", Limit: 1}
			first := searchMessages(t, engine, search)
			if len(first.Items) != 1 || first.Items[0].Message.ID != pending.Message.ID || first.NextCursor == "" {
				t.Fatalf("search disagreed with timeline order: %+v", first)
			}
			search.Cursor = first.NextCursor
			second := searchMessages(t, engine, search)
			if len(second.Items) != 1 || second.Items[0].Message.ID != neighbor.ID || second.NextCursor != "" {
				t.Fatalf("search keyset skipped the neighbor: %+v", second)
			}
			for _, m := range []domain.Message{latest[0], neighbor} {
				refs := application.SharedReferences(m.OriginalText)
				refs[0].ID, refs[0].RevisionID = migrationID(), migrationID()
				if err = s.IndexSharedContent(ctx, []domain.SharedContentMutation{{Message: m, References: refs, ParserVersion: application.SharedContentVersion}}); err != nil {
					t.Fatal(err)
				}
			}
			query := domain.SharedContentQuery{Type: "links", Limit: 1}
			firstLink, err := s.SharedContent(ctx, query)
			if err != nil || len(firstLink.Items) != 1 || firstLink.Items[0].Message.ID != pending.Message.ID || firstLink.NextCursor == "" {
				t.Fatalf("catalog disagreed with timeline order: %+v %v", firstLink, err)
			}
			query.Cursor = firstLink.NextCursor
			secondLink, err := s.SharedContent(ctx, query)
			if err != nil || len(secondLink.Items) != 1 || secondLink.Items[0].Message.ID != neighbor.ID || secondLink.NextCursor != "" {
				t.Fatalf("catalog keyset skipped the neighbor: %+v %v", secondLink, err)
			}
			upgraded := reopenSchedulingV21(t, s)
			latest, err = upgraded.Messages(ctx, chat.ID, "", 1)
			if err != nil || len(latest) != 1 || latest[0].ID != pending.Message.ID || latest[0].TimelineAt == nil || !latest[0].TimelineAt.Equal(position) {
				t.Fatalf("upgrade failed to backfill the release: %+v %v", latest, err)
			}
		})
	}
}

func TestSchedulingTimelineMigrationRepairsConversationSummary(t *testing.T) {
	for _, method := range []string{"immediate_rescheduled", "sole_immediate_rescheduled", "send_now_cancelled"} {
		t.Run(method, func(t *testing.T) {
			s, engine, chat := outboxFixture(t)
			ctx := t.Context()
			wantText, wantAt := "", chat.UpdatedAt
			if method != "sole_immediate_rescheduled" {
				neighbor := replySeed(t, s, chat, "visible message", time.Now().UTC().Add(-time.Minute), nil, nil)
				wantText, wantAt = neighbor.OriginalText, neighbor.CreatedAt
			}
			when := time.Now().UTC().Add(time.Hour)
			composition := domain.OutgoingComposition{OriginalText: "unpublished draft"}
			var initialSchedule *time.Time
			if method == "send_now_cancelled" {
				initialSchedule = &when
			}
			pending, err := engine.SendComposition(ctx, migrationID(), chat.ID, composition, initialSchedule)
			if err != nil {
				t.Fatal(err)
			}
			if initialSchedule == nil {
				if _, err = engine.UpdatePendingMessage(ctx, migrationID(), pending.Message.ID, pending.Message.RevisionID, nil, &when, false); err != nil {
					t.Fatal(err)
				}
			} else {
				released, err := engine.UpdatePendingMessage(ctx, migrationID(), pending.Message.ID, pending.Message.RevisionID, nil, nil, true)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = engine.CancelMessage(ctx, migrationID(), released.Message.ID, released.Message.RevisionID); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.Conversation(ctx, chat.ID)
			if err != nil || before.LastMessage != wantText || !before.UpdatedAt.Equal(wantAt) {
				t.Fatalf("removal left the sidebar stale: %+v %v", before, err)
			}
			// v21 left the previous summary untouched when the item was removed.
			if _, err = s.db.ExecContext(ctx, `UPDATE conversations SET payload = json_set(payload, '$.LastMessage', ?, '$.UpdatedAt', ?) WHERE id = ?`, composition.OriginalText, stamp(pending.Message.CreatedAt), chat.ID); err != nil {
				t.Fatal(err)
			}
			upgraded := reopenSchedulingV21(t, s)
			actual, err := upgraded.Conversation(ctx, chat.ID)
			if err != nil || actual.LastMessage != wantText || !actual.UpdatedAt.Equal(wantAt) || actual.RevisionID != before.RevisionID || actual.ActivitySequence != before.ActivitySequence {
				t.Fatalf("upgrade left the sidebar stale or rewrote conversation identity: %+v %v", actual, err)
			}
			conversations, err := upgraded.Conversations(ctx, application.ConversationQuery{Query: composition.OriginalText, Cursor: actual.ActivitySequence + 1, Limit: 10})
			if err != nil || len(conversations) != 0 {
				t.Fatalf("conversation search still exposes the unpublished text: %+v %v", conversations, err)
			}
			if _, err = upgraded.MessageContext(ctx, chat.ID, pending.Message.ID, 5); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("upgraded unpublished schedule still appears in context: %v", err)
			}
		})
	}
}

func reopenSchedulingV21(t *testing.T, s *Store) *Store {
	t.Helper()
	ctx := t.Context()
	migrateFixtureToVersion(t, s, schedulingMigration)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened
}
