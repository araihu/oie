package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreInvalidatesRetainedMessageWithNewReplyMetadata(t *testing.T) {
	t.Parallel()
	source, _, chat := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	const fallback = "> quoted https://example.invalid/secret\n"
	early := replySeed(t, source, chat, fallback+"answer", now.Add(-time.Hour), nil, nil)
	replySeed(t, source, chat, "latest shared message", now, nil, nil)
	if err := source.queries.MarkSharedContentIndexed(ctx, dbgen.MarkSharedContentIndexedParams{
		MessageID: string(early.ID), ParserVersion: 1, Caption: early.OriginalText,
	}); err != nil {
		t.Fatal(err)
	}
	dest := copyRestoreFixture(t, source)
	early.ReplyReference = &domain.ReplyReference{
		Target:   domain.MessageReference{Scope: "sender:bob@localhost", Author: "bob@localhost", ID: "quoted-id"},
		Fallback: []domain.TextRange{{Start: 0, End: len(fallback)}},
	}
	if err := source.ReceiveMessage(ctx, chat, early, migrationID()); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	_, sequence, err := dest.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	found, err := dest.Message(ctx, early.ID)
	if err != nil || found.Reply == nil || found.Reply.BodyText != "answer" || found.RevisionID != early.RevisionID {
		t.Fatalf("supplemental reply metadata did not apply to the same message revision: %+v, %v", found, err)
	}
	var staleCaptions int
	if err := dest.readDB.QueryRowContext(ctx, `SELECT count(*) FROM shared_content_state WHERE message_id=?`, early.ID).Scan(&staleCaptions); err != nil || staleCaptions != 0 {
		t.Fatalf("shared-content state survived new reply metadata: %d, %v", staleCaptions, err)
	}
	events, err := dest.Events(ctx, sequence, 100)
	if err != nil || len(events) != 1 || events[0].HistoryPruned == nil ||
		events[0].HistoryPruned.ConversationID != chat.ID || len(events[0].HistoryPruned.MessageIDs) != 0 {
		t.Fatalf("reply metadata did not refresh retained history: %+v, %v", events, err)
	}
	if err := dest.queries.MarkSharedContentIndexed(ctx, dbgen.MarkSharedContentIndexedParams{
		MessageID: string(early.ID), ParserVersion: 1, Caption: found.Reply.BodyText,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	_, after, err := dest.Snapshot(ctx)
	if err != nil || after != sequence+1 {
		t.Fatalf("reapplying the same metadata published extra events: %d, %v", after, err)
	}
	var caption string
	if err := dest.readDB.QueryRowContext(ctx, `SELECT caption FROM shared_content_state WHERE message_id=?`, early.ID).Scan(&caption); err != nil || caption != "answer" {
		t.Fatalf("reapplying the same metadata discarded refreshed shared content: %q, %v", caption, err)
	}
}
