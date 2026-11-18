package sqlite

import (
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreReconcilesInterruptedClaimsBeforeWire(t *testing.T) {
	t.Parallel()
	source, sourceEngine, chat := outboxFixture(t)
	ctx := t.Context()
	cancelled := queueBackupFixture(t, sourceEngine, chat.ID, "cancelled in backup", nil)
	uncertain := queueBackupFixture(t, sourceEngine, chat.ID, "uncertain in backup", nil)
	wireStarted := queueBackupFixture(t, sourceEngine, chat.ID, "destination crossed the wire boundary", nil)
	unwitnessed := queueBackupFixture(t, sourceEngine, chat.ID, "no new evidence", nil)
	destination := copyRestoreFixture(t, source)
	destinationEngine := testRestoreEngine(t, destination)
	echo := queueBackupFixture(t, destinationEngine, chat.ID, "archived by another engine", nil)
	retracted := queueBackupFixture(t, destinationEngine, chat.ID, "retracted without the archived body", nil)
	now := time.Now().UTC().Add(time.Hour)

	claims := make(map[domain.ID]domain.Message)
	for _, message := range []domain.Message{cancelled, uncertain, wireStarted, unwitnessed, echo, retracted} {
		claim, err := destination.ClaimDispatchMessage(ctx, message.ID, message.RevisionID, "", migrationID(), now, true)
		if err != nil {
			t.Fatal(err)
		}
		claims[message.ID] = claim
	}
	if err := destination.RecordOriginalWireAttempt(ctx, wireStarted.ID, claims[wireStarted.ID].RevisionID, application.Outgoing{OriginalText: wireStarted.OriginalText}, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := sourceEngine.CancelMessage(ctx, migrationID(), cancelled.ID, cancelled.RevisionID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id    domain.ID
		state string
	}{
		{id: uncertain.ID, state: "unknown"},
		{id: wireStarted.ID, state: "submitted"},
	} {
		if _, err := source.TransitionMessage(ctx, tc.id, "queued", "dispatching", migrationID(), now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := source.TransitionMessage(ctx, tc.id, "dispatching", tc.state, migrationID(), now, now); err != nil {
			t.Fatal(err)
		}
	}
	receiveBackupOwnDirectEcho(t, source, chat, echo)
	account, err := source.Account(ctx, chat.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.RecordMessageRetraction(ctx, chat.AccountID, "direct", chat.Address, application.IncomingRetraction{
		Scope: "xmpp:correct:sender:" + account.Address, RemoteID: string(retracted.ID), At: now,
	}); err != nil {
		t.Fatal(err)
	}
	manifest, archive, scratch := backupRestoreFixture(t, source, false)
	for range 2 {
		if _, err := destination.mergeBackup(ctx, archive, scratch, manifest, now); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		id    domain.ID
		state string
	}{
		{id: cancelled.ID, state: "cancelled"},
		{id: uncertain.ID, state: "unknown"},
		{id: retracted.ID, state: "unknown"},
	} {
		current, err := destination.Message(ctx, tc.id)
		if err != nil || current.SendState != tc.state {
			t.Fatalf("restore did not reconcile %s with %s evidence: %+v, %v", tc.id, tc.state, current, err)
		}
	}
	if current, err := destination.Message(ctx, echo.ID); err != nil ||
		(current.SendState != "submitted" && current.SendState != "unknown") {
		t.Fatalf("authenticated echo left the interrupted original executable: %+v, %v", current, err)
	}
	interrupted, err := destination.InterruptedMessages(ctx)
	if err != nil || len(interrupted) != 2 {
		t.Fatalf("only the original unaffected claims should reach startup recovery: %+v, %v", interrupted, err)
	}
	for _, current := range interrupted {
		if (current.ID != wireStarted.ID && current.ID != unwitnessed.ID) || current.RevisionID != claims[current.ID].RevisionID {
			t.Fatalf("restore changed an unaffected claim: %+v", current)
		}
		if err := destination.RecoverInterruptedMessage(ctx, current.ID, current.RevisionID, migrationID(), now); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := destination.PendingMessages(ctx, now); err != nil || len(pending) != 1 || pending[0].ID != unwitnessed.ID {
		t.Fatalf("startup did not requeue only the unwitnessed pre-wire claim: %+v, %v", pending, err)
	}
	if current, err := destination.Message(ctx, wireStarted.ID); err != nil || current.SendState != "unknown" {
		t.Fatalf("startup did not preserve uncertainty at the wire boundary: %+v, %v", current, err)
	}
}
