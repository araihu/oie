package sqlite

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreInterruptedEditMakesExactQueuedHTTPReplayUncertain(t *testing.T) {
	t.Parallel()
	source, editor, chat := outboxFixture(t)
	editor.SetProtocols(&editProtocol{})
	ctx := t.Context()
	original := ownSubmittedEdit(t, source, editor, chat, "locally accepted original")
	accepted := queuedEdit(t, editor, original.ID, "accepted before interruption")
	destination := copyRestoreFixture(t, source)
	receipt, err := destination.queries.GetOperation(ctx, string(accepted.Command.ID))
	if err != nil {
		t.Fatal(err)
	}
	dispatched, err := source.ClaimMessageEdit(ctx, accepted.Command.ID, migrationID(), time.Now())
	if err != nil || dispatched.Command.State != "dispatching" {
		t.Fatalf("expected committed dispatch claim before interruption: %+v, %v", dispatched, err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	backupErr := WriteProfileBackup(ctx, source.dir, file, false)
	if err := errors.Join(backupErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if summary, err := RestoreProfileBackup(ctx, destination.dir, path, migrationID()); err != nil || summary != (RestoreSummary{}) {
			t.Fatalf("interrupted edit changed imported message counts: %+v, %v", summary, err)
		}
	}
	restored, err := Open(ctx, destination.dir, migrationID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := restored.Close(); err != nil {
			t.Error(err)
		}
	}()
	if receiptAfter, err := restored.queries.GetOperation(ctx, string(accepted.Command.ID)); err != nil || receiptAfter != receipt {
		t.Fatalf("restore changed the immutable acceptance receipt: %+v, %v", receiptAfter, err)
	}
	edit, err := restored.MessageEdit(ctx, accepted.Command.ID)
	if err != nil || edit.Plan != dispatched.Plan || edit.ContentRevisionID != dispatched.ContentRevisionID ||
		edit.Command.RevisionID == accepted.Command.RevisionID || edit.Command.RevisionID == dispatched.Command.RevisionID {
		t.Fatalf("restoring the interrupted outcome changed the frozen edit or reused its state revision: %+v, %v", edit, err)
	}
	if pending, err := restored.PendingMessageEdits(ctx, time.Now().Add(time.Hour)); err != nil || len(pending) != 0 {
		t.Fatalf("restoring an interrupted edit left the same command executable: %+v, %v", pending, err)
	}

	handler, err := httpapi.New(testRestoreEngine(t, restored))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{
		"expectedRevisionId": string(accepted.Command.ExpectedRevisionID),
		"originalText":       accepted.Command.OriginalText,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/messages/"+string(original.ID)+"/edits", bytes.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", string(accepted.Command.ID))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var replay struct {
		State         string
		LastErrorCode string
	}
	if err := json.Unmarshal(response.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || replay.State != "unknown" || replay.LastErrorCode != "interrupted" {
		t.Fatalf("HTTP replay did not return the uncertain result: %d %s", response.Code, response.Body.String())
	}
}

func TestBackupRestoreIndependentCorrectionSettlesExactLocalEdit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, initial, want string
		differentBody       bool
	}{
		{name: "queued", initial: "queued", want: "submitted"},
		{name: "unknown", initial: "unknown", want: "submitted"},
		{name: "unrelated edit body", initial: "unknown", want: "unknown", differentBody: true},
		{name: "local terminal failure", initial: "failed", want: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination, editor, chat := outboxFixture(t)
			editor.SetProtocols(&editProtocol{})
			ctx := t.Context()
			original := ownSubmittedEdit(t, destination, editor, chat, "before independently witnessed correction")
			witness := copyRestoreFixture(t, destination)
			accepted := queuedEdit(t, editor, original.ID, "accepted and independently witnessed")
			if tc.initial != "queued" {
				finishEdit(t, destination, accepted.Command.ID, tc.initial)
			}
			local, err := destination.MessageEdit(ctx, accepted.Command.ID)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := destination.queries.GetOperation(ctx, string(accepted.Command.ID))
			if err != nil {
				t.Fatal(err)
			}
			observer := testRestoreEngine(t, witness)
			observer.SetProtocols(&outboundProtocol{})
			now := time.Now().UTC()
			const scope, author = "xmpp:correct:sender:alice@localhost", "alice@localhost"
			witnessedText := accepted.Command.OriginalText
			if tc.differentBody {
				witnessedText = "different body with the same remote correction ID"
			}
			// The other copy saw the correction in MAM without accepting a local command.
			if err := observer.Receive(ctx, application.Incoming{
				AccountID: chat.AccountID, Address: chat.Address, Kind: "direct", Historical: true, IsOwn: true,
				ProviderID: string(accepted.Command.ID), SenderAddress: author,
				CreatedAt: now, OriginalText: witnessedText, ArchiveScope: author, ArchiveID: "observed-standalone-correction",
				Correction: &domain.MessageCorrection{Scope: scope, Author: author, ID: string(accepted.Command.ID), TargetID: string(original.ID)},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := observer.MessageEdit(ctx, accepted.Command.ID); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("witness profile retained a local command: %v", err)
			}
			manifest, archive, scratch := backupRestoreFixture(t, witness, false)
			assertLocalEditRestoreRepeats(t, destination, manifest, archive, scratch, now)

			replay, err := editor.EditMessage(ctx, accepted.Command.ID, original.ID, accepted.Command.ExpectedRevisionID, accepted.Command.OriginalText)
			if err != nil || !replay.Replayed || replay.Command.State != tc.want {
				t.Fatalf("witness changed the wrong outcome or left the exact edit active: %+v, %v", replay, err)
			}
			got, err := destination.MessageEdit(ctx, accepted.Command.ID)
			if err != nil || got.Plan != local.Plan || got.ContentRevisionID != local.ContentRevisionID || got.WireBodyHash() != local.WireBodyHash() ||
				(got.Command.RevisionID == local.Command.RevisionID) != (tc.initial == tc.want) {
				t.Fatalf("witness changed the accepted plan or wrong state revision: %+v, %v", got, err)
			}
			if after, err := destination.queries.GetOperation(ctx, string(accepted.Command.ID)); err != nil || after != receipt {
				t.Fatalf("witness changed the acceptance receipt: %+v, %v", after, err)
			}
			if !tc.differentBody {
				if current, err := destination.Message(ctx, original.ID); err != nil || !current.Edited || current.OriginalText != witnessedText {
					t.Fatalf("verified source did not retain its historical correction: %+v, %v", current, err)
				}
			}
			if tc.want != "submitted" {
				return
			}
			if due, err := destination.PendingMessageEdits(ctx, now.Add(time.Hour)); err != nil || len(due) != 0 {
				t.Fatalf("confirmed correction remained executable: %+v, %v", due, err)
			}
			if target, err := editor.MessageEditTarget(ctx, original.ID); err != nil || !target.Eligible || target.OriginalText != witnessedText {
				t.Fatalf("confirmed correction did not release the edit target: %+v, %v", target, err)
			}
		})
	}
}
