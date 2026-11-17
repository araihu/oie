package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestorePromotesAnEmptyDestinationPlaceholder(t *testing.T) {
	source, sourceEngine, placeholder := outboxFixture(t)
	ctx := t.Context()
	preferences, err := sourceEngine.UpdateConversationPreferences(ctx, migrationID(), placeholder.ID,
		preferenceInput(placeholder, nil, preferenceBool(true)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceEngine.RemoveDirectFromChats(ctx, migrationID(), placeholder.ID, placeholder.AccountID, preferences.Conversation.RevisionID); err != nil {
		t.Fatal(err)
	}
	dest := copyRestoreFixture(t, source)
	prior := newerConversation(t, dest, placeholder.ID)
	roomEngine, room := promoteBackupRoomFixture(t, source, placeholder)
	now := time.Now().UTC()
	unlimited := 0
	if _, err := roomEngine.SetConversationRetention(ctx, room.ID, "0", &unlimited); err != nil {
		t.Fatal(err)
	}
	older := replySeed(t, source, room, "outside destination's room history window", now.AddDate(0, 0, -200), nil, nil)
	incoming := replySeed(t, source, room, "room history from backup", now, nil, nil)
	room = newerConversation(t, source, room.ID)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{Messages: 1}) {
		t.Fatalf("restore promoted room: %+v, %v", result, err)
	}
	got := newerConversation(t, dest, placeholder.ID)
	if got.Kind != "room" || got.RevisionID != room.RevisionID || got.Title != room.Title ||
		!got.Favorite || !got.RemovedFromChats || got.ActivitySequence != prior.ActivitySequence {
		t.Fatalf("room or destination preferences were lost: %+v", got)
	}
	if got, err := dest.Message(ctx, incoming.ID); err != nil || got.OriginalText != incoming.OriginalText {
		t.Fatalf("lost room history: %+v, %v", got, err)
	}
	if _, err := dest.Message(ctx, older.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("restore did not use the destination room retention policy: %v", err)
	}
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("reapplying the same archive changed the room: %+v, %v", result, err)
	}
}

func TestBackupRestoreRejectsPromotionWithLocalDirectTraffic(t *testing.T) {
	source, _, placeholder := outboxFixture(t)
	dest := copyRestoreFixture(t, source)
	_, room := promoteBackupRoomFixture(t, source, placeholder)
	now := time.Now().UTC()
	local := replySeed(t, dest, placeholder, "destination direct history", now, nil, nil)
	roomMessage := replySeed(t, source, room, "archive room history", now, nil, nil)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)

	if _, err := dest.mergeBackup(t.Context(), archive, scratch, manifest, now); err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
		t.Fatalf("incompatible room restore succeeded: %v", err)
	}
	if got := newerConversation(t, dest, placeholder.ID); got.Kind != "direct" {
		t.Fatalf("failed restore modified local conversation: %+v", got)
	}
	if got, err := dest.Message(t.Context(), local.ID); err != nil || got.OriginalText != local.OriginalText {
		t.Fatalf("failed restore modified local message: %+v, %v", got, err)
	}
	if _, err := dest.Message(t.Context(), roomMessage.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("room history survived failed restore: %v", err)
	}
}

func TestBackupRestoreScrubsExpiredQuoteWithLateMetadata(t *testing.T) {
	const secret = "source copy containing a private quotation"
	const quote = "> " + secret + "\n"
	const answer = "retained answer"
	for _, tc := range []struct {
		name      string
		localText string
		wantText  string
	}{
		{name: "shared revision", wantText: answer},
		{name: "new revision with the same body", localText: quote + answer, wantText: answer},
		{name: "new revision with different content", localText: "a newer local revision", wantText: "a newer local revision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, engine, chat := outboxFixture(t)
			ctx := t.Context()
			now := time.Now().UTC()
			ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "expired-archive-quote"}
			reply := replySeed(t, source, chat, quote+answer, now, nil, nil)
			dest := copyRestoreFixture(t, source)
			want := reply
			if tc.localText != "" {
				want = addBackupMessageRevision(t, dest, reply, tc.localText, now.Add(time.Second))
			}
			want.OriginalText = tc.wantText
			original := replySeed(t, source, chat, secret, now.AddDate(0, 0, -9), []domain.MessageReference{ref}, nil)
			reply.ReplyReference = &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: len(quote)}}}
			if err := source.ReceiveMessage(ctx, chat, reply, migrationID()); err != nil {
				t.Fatal(err)
			}
			days := 7
			if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
				t.Fatal(err)
			}
			if count, err := source.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || count != 1 {
				t.Fatalf("purge fixture: %d, %v", count, err)
			}
			if got, err := source.Message(ctx, reply.ID); err != nil || got.OriginalText != answer || got.Reply == nil || !got.Reply.OriginalExpired {
				t.Fatalf("source quote was not scrubbed: %+v, %v", got, err)
			}
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
				t.Fatal(err)
			}
			if _, err := dest.Message(ctx, original.ID); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("expired referenced message returned: %v", err)
			}
			got, err := dest.Message(ctx, reply.ID)
			if err != nil || got.RevisionID != want.RevisionID || got.OriginalText != want.OriginalText {
				t.Fatalf("restore exposed the quote or replaced the destination revision: %+v, %v", got, err)
			}
			if tc.wantText == answer && (got.Reply == nil || !got.Reply.OriginalExpired || got.Reply.BodyText != answer) {
				t.Fatalf("unexpected reply projection after redaction: %+v", got.Reply)
			}
			for _, table := range []string{"messages", "message_revisions", "conversation_revisions", "changes"} {
				var copies int
				if err := dest.readDB.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE instr(payload,?)>0", table), secret).
					Scan(&copies); err != nil || copies != 0 {
					t.Errorf("expired quote copies in %s: %d, %v", table, copies, err)
				}
			}
		})
	}
}

func TestBackupRestoreUsesDiscardedReferenceWithinDestinationRetention(t *testing.T) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "discarded-archive-quote"}
	const secret = "quotation from a message outside retention"
	const quote = "> " + secret + "\n"
	const answer = "retained answer"
	reply := replySeed(t, source, chat, quote+answer, now, nil, nil)
	restricted, wider, retained := copyRestoreFixture(t, source), copyRestoreFixture(t, source), copyRestoreFixture(t, source)
	original := replySeed(t, retained, chat, secret, now.AddDate(0, 0, -9), []domain.MessageReference{ref}, nil)
	olderManifest, olderArchive, olderScratch := backupRestoreFixture(t, retained, false)

	reply.ReplyReference = &domain.ReplyReference{Target: ref, Fallback: []domain.TextRange{{Start: 0, End: len(quote)}}}
	if err := source.ReceiveMessage(ctx, chat, reply, migrationID()); err != nil {
		t.Fatal(err)
	}
	days := 7
	if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	original.ReceivedAt = now
	if err := source.ReceiveMessage(ctx, chat, original, migrationID()); err != nil {
		t.Fatal(err)
	}
	if got, err := source.Message(ctx, reply.ID); err != nil || got.OriginalText != answer || got.Reply == nil || !got.Reply.OriginalExpired {
		t.Fatalf("source quote was not scrubbed after ingest discarded the target: %+v, %v", got, err)
	}
	if _, err := source.Message(ctx, original.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("archive ingest unexpectedly retained the original: %v", err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)

	for _, tc := range []struct {
		name      string
		store     *Store
		days      int
		wantScrub bool
	}{
		{name: "cutoff excludes original", store: restricted, days: 7, wantScrub: true},
		{name: "cutoff allows original", store: wider, days: 14},
		{name: "original already retained", store: retained, days: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destinationEngine := testRestoreEngine(t, tc.store)
			policy, err := destinationEngine.SetConversationRetention(ctx, chat.ID, "0", &tc.days)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tc.store.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
				t.Fatal(err)
			}
			got, err := tc.store.Message(ctx, reply.ID)
			if err != nil || got.RevisionID != reply.RevisionID {
				t.Fatalf("restore lost the reply revision: %+v, %v", got, err)
			}
			discarded, err := expiredReplyReference(ctx, tc.store.read, chat.ID, ref)
			if err != nil || discarded != tc.wantScrub {
				t.Fatalf("unexpected discarded reference at destination: %t, %v", discarded, err)
			}
			if tc.wantScrub {
				if got.OriginalText != answer || got.Reply == nil || !got.Reply.OriginalExpired {
					t.Fatalf("discarded target left a quoted body in the reply: %+v", got)
				}
				widenedDays := 14
				if _, err := destinationEngine.SetConversationRetention(ctx, chat.ID, policy.Revision, &widenedDays); err != nil {
					t.Fatal(err)
				}
			} else if got.OriginalText != quote+answer {
				t.Fatalf("restore erased a quote permitted by the destination: %+v", got)
			}
			if _, err := tc.store.mergeBackup(ctx, olderArchive, olderScratch, olderManifest, now); err != nil {
				t.Fatal(err)
			}
			if got, err := tc.store.Message(ctx, original.ID); err != nil || got.OriginalText != secret {
				t.Fatalf("discarded reference blocked recovery of the original: %+v, %v", got, err)
			}
			got, err = tc.store.Message(ctx, reply.ID)
			if err != nil || got.Reply == nil || !got.Reply.Available ||
				got.Reply.MessageID != original.ID || got.Reply.Excerpt != secret || got.Reply.BodyText != answer {
				t.Fatalf("reply did not project correctly after the original was recovered: %+v, %v", got.Reply, err)
			}
			if !tc.wantScrub && (got.OriginalText != quote+answer || got.RevisionID != reply.RevisionID) {
				t.Fatalf("recovering the fallback changed the destination wire body: %+v", got)
			}
		})
	}
}

func TestBackupRestoreRejectsConflictingScopedArchiveAlias(t *testing.T) {
	source, _, chat := outboxFixture(t)
	dest := copyRestoreFixture(t, source)
	ctx := t.Context()
	now := time.Now().UTC()
	incoming := domain.Message{
		ID: migrationID(), RevisionID: migrationID(), AccountID: chat.AccountID, ConversationID: chat.ID,
		OriginalText: "one archived message", CreatedAt: now, Historical: true, SendState: "received",
		ArchiveScope: chat.Address, ArchiveID: "message-id-from-mam-result",
	}
	if err := source.ReceiveMessage(ctx, chat, incoming, migrationID()); err != nil {
		t.Fatal(err)
	}
	incoming.ID, incoming.RevisionID = migrationID(), migrationID()
	if err := dest.ReceiveMessage(ctx, chat, incoming, migrationID()); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
		t.Fatalf("restore accepted two local identities for the same scoped archive message: %v", err)
	}
	items, err := testRestoreEngine(t, dest).Messages(ctx, chat.ID, "", 100)
	if err != nil || len(items) != 1 || items[0].ID != incoming.ID {
		t.Fatalf("failed merge changed history: %+v, %v", items, err)
	}
}
