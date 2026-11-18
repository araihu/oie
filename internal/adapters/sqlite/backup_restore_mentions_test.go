package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreReclassifiesQuoteOnlyRoomMention(t *testing.T) {
	t.Parallel()
	source, engine, chat := outboxFixture(t)
	engine.SetProtocols(&outboundProtocol{})
	room := outboundRoom(t, source, chat.AccountID, "room@rooms.localhost")
	ctx := t.Context()
	now := time.Now().UTC()
	const quote = "> Ana, oi\n"
	in := application.Incoming{
		AccountID: room.AccountID, Kind: "room", Address: room.Address,
		OwnRoomNickname: "Ana", ProviderID: "reply", OriginalText: quote + "Oi Bob!", CreatedAt: now,
	}
	if err := engine.Receive(ctx, in); err != nil {
		t.Fatal(err)
	}
	reply := correctionMessages(t, engine, room)[0]
	other := in
	other.ProviderID, other.OriginalText, other.CreatedAt = "already-read", "separate message", now.Add(time.Millisecond)
	if err := engine.Receive(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherMessage := correctionMessages(t, engine, room)[0]
	if otherMessage.ProviderID != other.ProviderID {
		t.Fatalf("fixture did not retain the separate newer message: %+v", otherMessage)
	}
	destination := copyRestoreFixture(t, source)
	destEngine := testRestoreEngine(t, destination)
	read, err := destEngine.MarkMessagesRead(ctx, migrationID(), room.ID, []domain.ID{otherMessage.ID})
	if err != nil {
		t.Fatal(err)
	}
	before := newerConversation(t, destination, room.ID)
	if !reply.MentionsYou || !before.UnreadMention || before.UnreadCount != 1 {
		t.Fatalf("fixture did not retain one unread quoted mention: %+v, %+v", reply, before)
	}

	in.OwnRoomNickname, in.Historical = "", true
	in.Reply = &domain.ReplyReference{
		Target:   domain.MessageReference{ID: "earlier"},
		Fallback: []domain.TextRange{{Start: 0, End: len(quote)}},
	}
	if err := engine.Receive(ctx, in); err != nil {
		t.Fatal(err)
	}
	if sourceReply, err := engine.Message(ctx, reply.ID); err != nil || sourceReply.MentionsYou || sourceReply.Reply == nil {
		t.Fatalf("live archive enrichment did not update the fixture: %+v, %v", sourceReply, err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	_, sequence, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	current, err := destination.Message(ctx, reply.ID)
	if err != nil || current.Reply == nil || current.Reply.BodyText != "Oi Bob!" || current.MentionsYou || current.RevisionID != reply.RevisionID {
		t.Fatalf("restore retained the quoted mention or changed the message revision: %+v, %v", current, err)
	}
	after := newerConversation(t, destination, room.ID)
	if after.UnreadMention || after.UnreadCount != 1 || after.RevisionID != before.RevisionID || after.ActivitySequence != before.ActivitySequence {
		t.Fatalf("restore did not clear just the unread mention: before %+v, after %+v", before, after)
	}
	if readAfter, err := destEngine.ConversationReadState(ctx, room.ID); err != nil || readAfter.RevisionID != read.State.RevisionID || readAfter.UnreadCount != 1 {
		t.Fatalf("restore changed destination read evidence: before %+v, after %+v, %v", read.State, readAfter, err)
	}
	events, err := destination.Events(ctx, sequence, 100)
	if err != nil {
		t.Fatal(err)
	}
	var refreshedSummary, refreshedHistory bool
	for _, event := range events {
		if event.Conversation != nil && event.Conversation.ID == room.ID && !event.Conversation.UnreadMention && event.Conversation.UnreadCount == 1 {
			refreshedSummary = true
		}
		if event.HistoryPruned != nil && event.HistoryPruned.ConversationID == room.ID && len(event.HistoryPruned.MessageIDs) == 0 {
			refreshedHistory = true
		}
	}
	if !refreshedSummary || !refreshedHistory {
		t.Fatalf("restore did not refresh the room summary and timeline: %+v", events)
	}
	_, sequence, err = destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
		t.Fatal(err)
	}
	if _, repeated, err := destination.Snapshot(ctx); err != nil || repeated != sequence {
		t.Fatalf("reapplying unchanged mention metadata published events: %d to %d, %v", sequence, repeated, err)
	}
}
