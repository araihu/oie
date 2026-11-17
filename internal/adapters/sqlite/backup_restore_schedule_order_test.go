package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
)

func TestBackupRestoreHistoricalSchedulePreservesDestinationActivity(t *testing.T) {
	source, engine, first := outboxFixture(t)
	ctx := t.Context()
	engine.SetProtocols(&outboundProtocol{})
	opened, err := engine.OpenConversation(ctx, migrationID(), first.AccountID, "backup-newer-chat@localhost")
	if err != nil {
		t.Fatal(err)
	}
	second := opened.Conversation
	due := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	scheduled := queueBackupFixture(t, engine, first.ID, "old scheduled send", &due)
	destination := copyRestoreFixture(t, source)
	sentAt := due.Add(time.Minute)
	replySeed(t, destination, first, "newer first-chat text", sentAt.Add(time.Hour), nil, nil)
	replySeed(t, destination, second, "newest chat activity", sentAt.Add(2*time.Hour), nil, nil)
	firstBefore, secondBefore := newerConversation(t, destination, first.ID), newerConversation(t, destination, second.ID)
	if firstBefore.ActivitySequence >= secondBefore.ActivitySequence {
		t.Fatalf("destination fixture does not place the second conversation first: %+v, %+v", firstBefore, secondBefore)
	}
	if _, err := source.TransitionMessage(ctx, scheduled.ID, "queued", "dispatching", migrationID(), sentAt, sentAt); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, scheduled.ID, "dispatching", "submitted", migrationID(), sentAt, sentAt); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	_, sequence, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restoreAt := sentAt.Add(3 * time.Hour)
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, restoreAt); err != nil {
		t.Fatal(err)
	}
	current, err := destination.Message(ctx, scheduled.ID)
	if err != nil || current.SendState != "submitted" || !current.TimelineTime().Equal(sentAt) {
		t.Fatalf("restore did not publish the scheduled send at its actual time: %+v, %v", current, err)
	}
	firstAfter, secondAfter := newerConversation(t, destination, first.ID), newerConversation(t, destination, second.ID)
	if firstAfter.ActivitySequence != firstBefore.ActivitySequence || secondAfter.ActivitySequence != secondBefore.ActivitySequence ||
		firstAfter.RevisionID != firstBefore.RevisionID || firstAfter.LastMessage != firstBefore.LastMessage {
		t.Fatalf("historical schedule changed destination activity or revision: before %+v, after %+v", firstBefore, firstAfter)
	}
	page, err := testRestoreEngine(t, destination).Conversations(ctx, "", application.ConversationQuery{Limit: 10})
	if err != nil || len(page) != 2 || page[0].ID != second.ID || page[1].ID != first.ID {
		t.Fatalf("historical schedule reordered conversations: %+v, %v", page, err)
	}
	events, err := destination.Events(ctx, sequence, 100)
	if err != nil || len(events) != 2 || events[0].Message == nil || events[0].Message.ID != scheduled.ID ||
		events[1].HistoryPruned == nil || events[1].HistoryPruned.ConversationID != first.ID {
		t.Fatalf("restore did not refresh the existing history: %+v, %v", events, err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, restoreAt); err != nil {
		t.Fatal(err)
	}
	if _, repeated, err := destination.Snapshot(ctx); err != nil || repeated != sequence+int64(len(events)) {
		t.Fatalf("reapplying scheduled evidence published extra events: %d, %v", repeated, err)
	}
}
