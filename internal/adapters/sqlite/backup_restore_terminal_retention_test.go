package sqlite

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreReservesOperationsForTerminalSendsOutsideRetention(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		scheduled   bool
		forwarded   bool
	}{
		{name: "submitted", state: "submitted", scheduled: true},
		{name: "failed", state: "failed"},
		{name: "cancelled", state: "cancelled"},
		{name: "forwarded", state: "submitted", forwarded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testBackupRestoreTerminalOutsideRetention(t, tc.state, tc.scheduled, tc.forwarded)
		})
	}
}

func testBackupRestoreTerminalOutsideRetention(t *testing.T, state string, scheduled, forwarded bool) {
	source, engine, chat := outboxFixture(t)
	ctx := t.Context()
	var original domain.Message
	if forwarded {
		original = replySeed(t, source, chat, "the original to forward", time.Now().UTC(), nil, nil)
	}
	dest := copyRestoreFixture(t, source)
	destEngine := testRestoreEngine(t, dest)
	days := 1
	if _, err := destEngine.SetConversationRetention(ctx, chat.ID, "0", &days); err != nil {
		t.Fatal(err)
	}
	key := migrationID()
	operationKeys := []domain.ID{key}
	composition := domain.OutgoingComposition{OriginalText: "private composition outside retention"}
	var due *time.Time
	if scheduled {
		at := time.Now().UTC().Add(time.Hour)
		due = &at
	}
	send := func(e *application.Engine) (application.MessageResult, error) {
		if forwarded {
			return e.ForwardMessage(ctx, key, original.ID, original.RevisionID, chat.ID)
		}
		return e.SendComposition(ctx, key, chat.ID, composition, due)
	}
	accepted, err := send(engine)
	if err != nil {
		t.Fatal(err)
	}
	message := accepted.Message
	if scheduled {
		editKey, sendNowKey := migrationID(), migrationID()
		later := due.Add(time.Hour)
		edited, err := engine.UpdatePendingMessage(ctx, editKey, message.ID, message.RevisionID, nil, &later, false)
		if err != nil {
			t.Fatal(err)
		}
		activated, err := engine.UpdatePendingMessage(ctx, sendNowKey, message.ID, edited.Message.RevisionID, nil, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		message = activated.Message
		operationKeys = append(operationKeys, editKey, sendNowKey)
	}
	pendingManifest, pendingArchive, pendingScratch := backupRestoreFixture(t, source, false)
	terminalAt := message.CreatedAt.Add(time.Minute)
	if state == "cancelled" {
		cancelKey := migrationID()
		if _, err := engine.CancelMessage(ctx, cancelKey, message.ID, message.RevisionID); err != nil {
			t.Fatal(err)
		}
		operationKeys = append(operationKeys, cancelKey)
	} else {
		if _, err := source.TransitionMessage(ctx, message.ID, "queued", "dispatching", migrationID(), terminalAt, terminalAt); err != nil {
			t.Fatal(err)
		}
		if _, err := source.TransitionMessage(ctx, message.ID, "dispatching", state, migrationID(), terminalAt, terminalAt); err != nil {
			t.Fatal(err)
		}
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	now := terminalAt.Add(48 * time.Hour)
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("old terminal snapshot restored content or executable sends: %+v, %v", result, err)
	}
	if _, err := dest.Message(ctx, message.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("old terminal body was retained: %v", err)
	}
	if _, err := send(destEngine); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("retrying the original operation did not find its expired receipt: %v", err)
	}
	for _, key := range operationKeys {
		want, err := source.queries.GetOperation(ctx, string(key))
		if err != nil || want.ResourceID != string(message.ID) || want.Result == "null" {
			t.Fatalf("source operation fixture is missing its receipt: %+v, %v", want, err)
		}
		want.Result = "null"
		if got, err := dest.queries.GetOperation(ctx, string(key)); err != nil || got != want {
			t.Fatalf("restore did not reserve %s without the body: %+v, %v", want.Kind, got, err)
		}
	}
	if result, err := dest.mergeBackup(ctx, pendingArchive, pendingScratch, pendingManifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("stale pending snapshot revived the terminal send: %+v, %v", result, err)
	}
	if items, err := dest.RecoveredSends(ctx); err != nil || len(items) != 0 {
		t.Fatalf("terminal evidence left paused sends: %+v, %v", items, err)
	}
	if items, err := dest.PendingMessages(ctx, now); err != nil || len(items) != 0 {
		t.Fatalf("terminal evidence left dispatchable sends: %+v, %v", items, err)
	}
}
