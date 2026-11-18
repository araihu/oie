package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreKeepsExpiredCorrectionBranchExcludedWhenItsRootIsNew(t *testing.T) {
	t.Parallel()
	source, sender, chat := outboxFixture(t)
	ctx := t.Context()
	sender.SetProtocols(&outboundProtocol{})
	now := time.Now().UTC()
	const scope, actor = "xmpp:correct:sender:bob@localhost", "bob@localhost"
	const rootBody, siblingBody = "the original found on another branch", "the independent sibling edit"
	const deletedBody = "a correction descending from the deleted edit"

	// A correction can be observed before its original is discovered.
	receiveCorrection(t, sender, chat, "intermediate-edit", "a subsequently expired orphan", "older-root", scope, actor, now.Add(-48*time.Hour))
	middle := correctionMessages(t, sender, chat)[0]
	destination := copyRestoreFixture(t, source)
	retained := replySeed(t, destination, chat, "previously retained destination revision", now, nil, nil)
	target := testRestoreEngine(t, destination)
	days := 1
	if _, err := target.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	if n, err := destination.PurgeExpiredHistory(ctx, now, func() (domain.ID, error) { return migrationID(), nil }); err != nil || n != 1 {
		t.Fatalf("destination retention did not expire the orphan correction: %d, %v", n, err)
	}
	if _, err := destination.MessageSource(ctx, middle.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("expired correction still available: %v", err)
	}

	receiveCorrection(t, sender, chat, "older-root", rootBody, "", scope, actor, now.Add(-49*time.Hour))
	receiveCorrection(t, sender, chat, "sibling-edit", siblingBody, "older-root", scope, actor, now.Add(-2*time.Hour))
	receiveCorrection(t, sender, chat, "recent-edited-descendant", deletedBody, "intermediate-edit", scope, actor, now.Add(-time.Hour))
	full := correctionMessages(t, sender, chat)
	if len(full) != 1 || !full[0].Edited || full[0].ProjectionSourceID == "" || full[0].OriginalText != deletedBody {
		t.Fatalf("source does not have the verified correction chain: %+v", full)
	}
	originalID, descendantID := full[0].ID, full[0].ProjectionSourceID
	independent := replySeed(t, source, chat, "an unrelated eligible message", now.Add(-time.Minute), nil, nil)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Messages != 1 {
		t.Fatalf("narrow retention did not restore only the unrelated recent history: %+v, %v", result, err)
	}
	if _, err := destination.Message(ctx, originalID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("root was already present before widening retention: %v", err)
	}
	retention, err := destination.ConversationRetention(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	wider := 7
	if _, err := target.SetConversationRetention(ctx, chat.ID, retention.Revision, &wider); err != nil {
		t.Fatal(err)
	}
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result.Messages != 1 {
		t.Fatalf("widening should add only the corrected original: %+v, %v", result, err)
	}
	if raw, err := destination.MessageSource(ctx, originalID); err != nil || raw.OriginalText != rootBody {
		t.Fatalf("fresh root was lost or altered: %+v, %v", raw, err)
	}
	if visible, err := destination.Message(ctx, originalID); err != nil || !visible.Edited || visible.OriginalText != siblingBody {
		t.Fatalf("the verified sibling edit did not survive: %+v, %v", visible, err)
	}
	for _, id := range []domain.ID{middle.ID, descendantID} {
		if _, err := destination.MessageSource(ctx, id); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("the previously expired branch returned through %s: %v", id, err)
		}
	}
	for _, item := range []domain.Message{retained, independent} {
		if got, err := destination.MessageSource(ctx, item.ID); err != nil || got.RevisionID != item.RevisionID || got.OriginalText != item.OriginalText {
			t.Fatalf("unrelated history changed: %+v, %v", got, err)
		}
	}
	_, before, err := destination.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("repeat merge did not remain stable: %+v, %v", result, err)
	}
	if _, after, err := destination.Snapshot(ctx); err != nil || before != after {
		t.Fatalf("repeat merge emitted events from %d to %d: %v", before, after, err)
	}
}
