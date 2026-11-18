package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRecoversExactFallbackFromOlderArchive(t *testing.T) {
	t.Parallel()
	base, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	const answer = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const middle = "[quoted middle 🍰]"
	trailing := strings.Join(strings.Split(answer, ""), "#") + "#"
	raw := answer[:13] + middle + answer[13:] + trailing
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "fallback-two-ranges"}
	reply := replySeed(t, base, chat, raw, now, nil, nil)
	newer, older, destination := copyRestoreFixture(t, base), copyRestoreFixture(t, base), copyRestoreFixture(t, base)
	reply.ReplyReference = &domain.ReplyReference{
		Target: ref,
		Fallback: []domain.TextRange{
			{Start: 13, End: 13 + utf8.RuneCountInString(middle)},
			{Start: utf8.RuneCountInString(raw) - utf8.RuneCountInString(trailing), End: utf8.RuneCountInString(raw)},
		},
	}
	if got, _ := domain.ReplyBody(raw, reply.ReplyReference.Fallback); got != answer {
		t.Fatalf("invalid original fallback fixture: %q", got)
	}
	if err := older.ReceiveMessage(ctx, chat, reply, migrationID()); err != nil {
		t.Fatal(err)
	}
	original := replySeed(t, older, chat, "retained quoted original", now.AddDate(0, 0, -9), []domain.MessageReference{ref}, nil)
	oldManifest, oldArchive, oldScratch := backupRestoreFixture(t, older, false)
	if err := newer.ReceiveMessage(ctx, chat, reply, migrationID()); err != nil {
		t.Fatal(err)
	}
	short, long := 7, 14
	if _, err := testRestoreEngine(t, newer).SetConversationRetention(ctx, chat.ID, "0", &short); err != nil {
		t.Fatal(err)
	}
	if _, err := testRestoreEngine(t, destination).SetConversationRetention(ctx, chat.ID, "0", &long); err != nil {
		t.Fatal(err)
	}
	original.ReceivedAt = now
	if err := newer.ReceiveMessage(ctx, chat, original, migrationID()); err != nil {
		t.Fatal(err)
	}
	if got, err := newer.Message(ctx, reply.ID); err != nil || got.OriginalText != answer || got.Reply == nil || !got.Reply.OriginalExpired {
		t.Fatalf("newer archive fixture did not redact the quote: %+v, %v", got, err)
	}
	newManifest, newArchive, newScratch := backupRestoreFixture(t, newer, false)
	if _, err := destination.mergeBackup(ctx, newArchive, newScratch, newManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, oldArchive, oldScratch, oldManifest, now); err != nil {
		t.Fatal(err)
	}
	got, err := destination.Message(ctx, reply.ID)
	if err != nil || got.Reply == nil || got.Reply.MessageID != original.ID || !got.Reply.Available ||
		got.Reply.BodyText != answer || got.OriginalText != raw || got.RevisionID != reply.RevisionID {
		t.Fatalf("restore lost the original fallback or changed the wire body: %+v, %v", got, err)
	}
	_, sequence, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, oldArchive, oldScratch, oldManifest, now); err != nil {
		t.Fatal(err)
	}
	if _, repeated, err := destination.Snapshot(ctx); err != nil || repeated != sequence {
		t.Fatalf("reapplying exact fallback metadata published extra events: %d to %d, %v", sequence, repeated, err)
	}
}

func TestBackupRestoreProjectsReplyWhenDestinationRetainedOriginal(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	ref := domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "original-with-policy-difference"}
	const excerpt = "the retained original 🍰"
	const quote = "> " + excerpt + "\n"
	const answer = "the authored reply"
	original := replySeed(t, source, chat, excerpt, now.AddDate(0, 0, -10), []domain.MessageReference{ref}, nil)
	reply := replySeed(t, source, chat, quote+answer, now, nil, nil)
	destination := copyRestoreFixture(t, source)
	reply.ReplyReference = &domain.ReplyReference{
		Target:   ref,
		Fallback: []domain.TextRange{{Start: 0, End: utf8.RuneCountInString(quote)}},
	}
	if err := source.ReceiveMessage(ctx, chat, reply, migrationID()); err != nil {
		t.Fatal(err)
	}
	days := 7
	if _, err := engine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if removed, err := source.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || removed != 1 {
		t.Fatalf("source did not expire the original: %d, %v", removed, err)
	}
	if current, err := source.Message(ctx, reply.ID); err != nil || current.OriginalText != answer || current.Reply == nil || !current.Reply.OriginalExpired {
		t.Fatalf("source did not redact the reply: %+v, %v", current, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	current, err := destination.Message(ctx, reply.ID)
	if err != nil || current.Reply == nil || !current.Reply.Available || current.Reply.MessageID != original.ID ||
		current.Reply.Excerpt != excerpt || current.Reply.BodyText != answer {
		t.Fatalf("restore duplicated the quotation in the reply body: %+v, %v", current.Reply, err)
	}
	if current.OriginalText != quote+answer || current.RevisionID != reply.RevisionID {
		t.Fatalf("restoring the projection overwrote the saved destination wire body: %+v", current)
	}
	_, before, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if _, after, err := destination.Snapshot(ctx); err != nil || after != before {
		t.Fatalf("reapplying the same fallback published extra events: %d to %d, %v", before, after, err)
	}
	if _, err := testRestoreEngine(t, destination).SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if removed, err := destination.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || removed != 1 {
		t.Fatalf("destination did not expire the original: %d, %v", removed, err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.Message(ctx, original.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("restore revived the expired original: %v", err)
	}
	if current, err := destination.Message(ctx, reply.ID); err != nil || current.OriginalText != answer || current.RevisionID != reply.RevisionID ||
		current.Reply == nil || !current.Reply.OriginalExpired || current.Reply.Excerpt != "" {
		t.Fatalf("restored fallback did not allow later local redaction: %+v, %v", current, err)
	}
}

func TestRestoredReplyFallbackRequiresExistingWireText(t *testing.T) {
	for _, tc := range []struct{ raw, answer string }{
		{"> 🍰\nThe answer", "The answer"},
		{"one💬--two🍰--three", "one--two--three"},
		{"before >🍰 after", "before after"},
	} {
		spans := restoredReplyFallback(tc.raw, tc.answer)
		if got, _ := domain.ReplyBody(tc.raw, spans); len(spans) == 0 || got != tc.answer {
			t.Errorf("restored fallback for %q: %+v, body %q", tc.raw, spans, got)
		}
	}
	if spans := restoredReplyFallback("original reply", "changed reply"); len(spans) != 0 {
		t.Fatalf("replacement text was accepted as an existing fallback: %+v", spans)
	}
}
