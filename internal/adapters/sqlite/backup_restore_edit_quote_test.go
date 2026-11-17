package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreEditedReplyScrubsExpiredQuote(t *testing.T) {
	ctx := t.Context()
	source, sender, chat := outboxFixture(t)
	protocol := &editProtocol{}
	protocol.wireReply = true
	sender.SetProtocols(protocol)
	const secret = "expired quoted text belongs only to the old target"
	const initial = "my reply"
	now := time.Now().UTC()
	peer := domain.MessageReference{ID: "older-peer-message", Scope: "sender:" + chat.Address, Author: chat.Address}
	if err := sender.Receive(ctx, application.Incoming{
		AccountID: chat.AccountID, Address: chat.Address, Historical: true, OriginalText: secret,
		ProviderID: peer.ID, References: []domain.MessageReference{peer}, CreatedAt: now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	history, err := source.Messages(ctx, chat.ID, "", 10)
	if err != nil || len(history) != 1 {
		t.Fatalf("expected the old quoted target: %+v, %v", history, err)
	}
	quotedID := history[0].ID
	destination := copyRestoreFixture(t, source)
	sent, err := sender.SendComposition(ctx, migrationID(), chat.ID, domain.OutgoingComposition{
		OriginalText: initial, ReplyTo: quotedID,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	replyID := sent.Message.ID
	now = time.Now().UTC()
	if _, err := source.TransitionMessage(ctx, replyID, "queued", "dispatching", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := source.TransitionMessage(ctx, replyID, "dispatching", "submitted", migrationID(), now, now); err != nil {
		t.Fatal(err)
	}

	type editFixture struct {
		pending domain.PendingMessageEdit
		final   domain.MessageEditCommand
	}
	var edits []editFixture
	for _, attempt := range []struct{ body, state string }{
		{"my first revised reply", "submitted"},
		{"my second revised reply", "submitted"},
		{"my failed third revision", "failed"},
	} {
		edit := queuedEdit(t, sender, replyID, attempt.body)
		pending, err := source.MessageEdit(ctx, edit.Command.ID)
		if err != nil || pending.Reply == nil || pending.Reply.TargetID != quotedID || pending.Reply.Reference == nil ||
			len(pending.Reply.Fallback) == 0 || !strings.Contains(pending.WireBody(), secret) {
			t.Fatalf("test did not record the private quote before expiry: %+v, %v", pending, err)
		}
		edits = append(edits, editFixture{pending: pending, final: finishEdit(t, source, edit.Command.ID, attempt.state)})
	}
	manifest, archived, scratch := backupRestoreFixture(t, source, false)

	expireQuotedTarget := func(store *Store) {
		t.Helper()
		settings := testRestoreEngine(t, store)
		days := 1
		policy, err := settings.SetConversationRetention(ctx, chat.ID, "0", &days)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := store.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || count != 1 {
			t.Fatalf("expected to expire only the old quoted target: %d, %v", count, err)
		}
		days = 7
		if _, err := settings.SetConversationRetention(ctx, chat.ID, policy.Revision, &days); err != nil {
			t.Fatal(err)
		}
	}
	check := func(store *Store) {
		t.Helper()
		engine := testRestoreEngine(t, store)
		for _, edit := range edits {
			pending, err := store.MessageEdit(ctx, edit.final.ID)
			if err != nil || pending.Reply == nil || pending.Reply.TargetID != quotedID ||
				pending.Reply.Excerpt != "" || pending.Reply.SenderName != "" || pending.Reply.Reference != nil || len(pending.Reply.Fallback) != 0 ||
				pending.WireText != "" || pending.WireBody() != edit.final.OriginalText ||
				pending.WireBodySHA256 != edit.pending.WireBodyHash() || pending.MatchesWireBody(pending.WireBody()) ||
				pending.Plan != edit.pending.Plan || pending.Command.RevisionID != edit.final.RevisionID {
				t.Fatalf("restored edit retained private data or lost authenticated proof: %+v, %v", pending, err)
			}
			replayed, err := engine.EditMessage(ctx, edit.final.ID, replyID, edit.final.ExpectedRevisionID, edit.final.OriginalText)
			if err != nil || !replayed.Replayed || replayed.Command.State != edit.final.State {
				t.Fatalf("expired quote prevented replay of the original receipt: %+v, %v", replayed, err)
			}
		}
		original, err := store.MessageSource(ctx, replyID)
		if err != nil || original.OriginalText != initial {
			t.Fatalf("retained reply changed its authored text: %+v, %v", original, err)
		}
		current, err := store.Message(ctx, replyID)
		if err != nil || !current.Edited || current.OriginalText != edits[1].final.OriginalText || current.Reply == nil ||
			!current.Reply.OriginalExpired || current.Reply.Excerpt != "" {
			t.Fatalf("corrected reply retained its expired quote: %+v, %v", current, err)
		}
		if head, err := store.queries.GetContentHead(ctx, string(replyID)); err != nil || strings.Contains(head.Payload, secret) {
			t.Fatalf("stored correction projection retained the expired quote: %+v, %v", head, err)
		}
		revisions := editHistory(t, engine, replyID, "", 20)
		if len(revisions.Items) != 3 || revisions.Items[0].OriginalText != edits[1].final.OriginalText ||
			len(revisions.Items[0].ParentRevisionIDs) != 1 || revisions.Items[0].ParentRevisionIDs[0] != revisions.Items[1].RevisionID ||
			revisions.Items[1].OriginalText != edits[0].final.OriginalText {
			t.Fatalf("expired quote changed the accepted edit ancestry: %+v", revisions)
		}
	}

	expireQuotedTarget(destination)
	assertLocalEditRestoreRepeats(t, destination, manifest, archived, scratch, now)
	check(destination)
	relayedManifest, relayed, relayedScratch := backupRestoreFixture(t, destination, false)
	relay := openLegacyDownloadDestination(t)
	assertLocalEditRestoreRepeats(t, relay, relayedManifest, relayed, relayedScratch, now)
	check(relay)
	assertLocalEditRestoreRepeats(t, relay, manifest, archived, scratch, now)
	check(relay)

	expireQuotedTarget(source)
	check(source)
}
