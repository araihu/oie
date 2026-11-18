package sqlite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreRoomCorrectionConflictKeepsLocalHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source, editor, placeholder := outboxFixture(t)
	room := outboundRoom(t, source, placeholder.AccountID, "group@rooms.localhost")
	author := room.Address + "/Tester"
	scope := fmt.Sprintf("xmpp:correct:room:%s:%s", room.Address, bodyHash(author+"\x00"+"own-occupant-id"))
	protocol := &roomEditWitnessProtocol{scope: scope, author: author}
	editor.SetProtocols(protocol)
	joined, err := editor.JoinRoom(ctx, migrationID(), room.ID, "Tester", "")
	if err != nil {
		t.Fatal(err)
	}
	protocol.intent = joined.Session.IntentRevisionID
	original := ownSubmittedEdit(t, source, editor, room, "the editable room original")
	witness := copyRestoreFixture(t, source)
	edit := queuedEdit(t, editor, original.ID, "the locally submitted correction")
	finishEdit(t, source, edit.Command.ID, "submitted")
	observer := testRestoreEngine(t, witness)
	observer.SetProtocols(&outboundProtocol{})
	at := time.Now().UTC()
	const different = "the same authenticated correction identity with another body"
	const archiveKey = "independently-witnessed-correction"
	receive := func(id, target domain.ID, text, key string) {
		t.Helper()
		if err := observer.Receive(ctx, application.Incoming{
			AccountID: room.AccountID, Address: room.Address, Kind: "room", IsOwn: true, Historical: true,
			CanonicalID: id, ProviderID: roomCorrectionProviderID(author, string(id)), SenderName: "Tester", SenderAddress: "alice@localhost",
			CreatedAt: at, OriginalText: text, ArchiveScope: room.Address, ArchiveID: key,
			WireMentions: domain.WireMentionsAbsent,
			Correction:   &domain.MessageCorrection{ID: string(id), TargetID: string(target), Scope: scope, Author: author},
		}); err != nil {
			t.Fatal(err)
		}
	}
	receive(original.ID, "", original.OriginalText, "witnessed-original")
	receive(edit.Command.ID, original.ID, different, archiveKey)
	alias := dbgen.GetArchiveAliasParams{AccountID: string(room.AccountID), Scope: room.Address, ArchiveID: archiveKey}
	witnessID, err := witness.queries.GetArchiveAlias(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}

	fromNative, nativeDB, nativeScratch := backupRestoreFixture(t, source, false)
	fromWitness, witnessDB, witnessScratch := backupRestoreFixture(t, witness, false)
	for _, tc := range []struct {
		name           string
		store          *Store
		manifest       backupManifest
		database, dir  string
		oldBody        string
		oldSource      domain.ID
		keepArchiveKey bool
	}{
		{"native first", source, fromWitness, witnessDB, witnessScratch, edit.Command.OriginalText, edit.Command.ID, false},
		{"witness first", witness, fromNative, nativeDB, nativeScratch, different, domain.ID(witnessID), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, err := tc.store.Messages(ctx, room.ID, "", 10)
			if err != nil || len(before) != 1 || !before[0].Edited || before[0].OriginalText != tc.oldBody || before[0].ProjectionSourceID != tc.oldSource {
				t.Fatalf("invalid local correction history: %+v, %v", before, err)
			}
			raw, err := tc.store.MessageSource(ctx, tc.oldSource)
			if err != nil {
				t.Fatal(err)
			}
			_, sequence, err := tc.store.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tc.store.mergeBackup(ctx, tc.database, tc.dir, tc.manifest, at); err == nil ||
				!strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
				t.Fatalf("restore accepted competing bodies for one authenticated correction: %v", err)
			}
			if after, err := tc.store.Messages(ctx, room.ID, "", 10); err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("failed restore changed local history: %+v, %v", after, err)
			}
			if after, err := tc.store.MessageSource(ctx, tc.oldSource); err != nil || !reflect.DeepEqual(after, raw) {
				t.Fatalf("failed restore changed the retained revision: %+v, %v", after, err)
			}
			if _, after, err := tc.store.Snapshot(ctx); err != nil || after != sequence {
				t.Fatalf("failed restore emitted events: %d → %d, %v", sequence, after, err)
			}
			if tc.keepArchiveKey {
				if owner, err := tc.store.queries.GetArchiveAlias(ctx, alias); err != nil || owner != witnessID {
					t.Fatalf("failed restore moved the retained archive key: %q, %v", owner, err)
				}
			}
		})
	}
}

func TestBackupRestoreNativeCorrectionConflictKeepsLocalHistory(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind string
		equivalent bool
	}{
		{name: "direct conflict", kind: "direct"},
		{name: "room conflict", kind: "room"},
		{name: "equivalent direct corrections", kind: "direct", equivalent: true},
		{name: "equivalent room corrections", kind: "room", equivalent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			source, editor, chat := outboxFixture(t)
			var protocol application.Protocols = &editProtocol{}
			if tc.kind == "room" {
				chat = outboundRoom(t, source, chat.AccountID, "group@rooms.localhost")
				author := chat.Address + "/Tester"
				roomProtocol := &roomEditWitnessProtocol{
					scope:  "xmpp:correct:room:" + chat.Address + ":" + bodyHash(author+"\x00"+"own-occupant-id"),
					author: author,
				}
				editor.SetProtocols(roomProtocol)
				joined, err := editor.JoinRoom(ctx, migrationID(), chat.ID, "Tester", "")
				if err != nil {
					t.Fatal(err)
				}
				roomProtocol.intent = joined.Session.IntentRevisionID
				protocol = roomProtocol
			}
			editor.SetProtocols(protocol)
			original := ownSubmittedEdit(t, source, editor, chat, "common original")
			target, err := editor.MessageEditTarget(ctx, original.ID)
			if err != nil || !target.Eligible {
				t.Fatalf("expected the same editable original on both copies: %+v, %v", target, err)
			}
			destination := copyRestoreFixture(t, source)
			otherEditor := testRestoreEngine(t, destination)
			otherEditor.SetProtocols(protocol)
			key := migrationID()
			post := func(engine *application.Engine, body string) {
				t.Helper()
				handler, err := httpapi.New(engine)
				if err != nil {
					t.Fatal(err)
				}
				input, err := json.Marshal(map[string]string{
					"expectedRevisionId": string(target.CurrentRevisionID),
					"originalText":       body,
				})
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "/api/v1/messages/"+string(original.ID)+"/edits", bytes.NewReader(input)).WithContext(ctx)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", string(key))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusAccepted {
					t.Fatalf("copy did not independently accept the correction: %d %s", response.Code, response.Body.String())
				}
			}
			const primary = "correction accepted on the first copy"
			other := primary
			if !tc.equivalent {
				other = "different correction accepted with the same key"
			}
			post(editor, primary)
			post(otherEditor, other)
			finishEdit(t, source, key, "submitted")
			finishEdit(t, destination, key, "submitted")
			fromSource, sourceDB, sourceScratch := backupRestoreFixture(t, source, false)
			fromDestination, destinationDB, destinationScratch := backupRestoreFixture(t, destination, false)

			for _, order := range []struct {
				store             *Store
				manifest          backupManifest
				database, scratch string
				body              string
			}{
				{destination, fromSource, sourceDB, sourceScratch, other},
				{source, fromDestination, destinationDB, destinationScratch, primary},
			} {
				before, err := order.store.Messages(ctx, chat.ID, "", 10)
				if err != nil || len(before) != 1 || !before[0].Edited || before[0].OriginalText != order.body {
					t.Fatalf("invalid local timeline before merge: %+v, %v", before, err)
				}
				receipt, err := order.store.queries.GetOperation(ctx, string(key))
				if err != nil {
					t.Fatal(err)
				}
				raw, err := order.store.MessageSource(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				_, sequence, err := order.store.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				if tc.equivalent {
					assertLocalEditRestoreRepeats(t, order.store, order.manifest, order.database, order.scratch, now)
				} else if _, err := order.store.mergeBackup(ctx, order.database, order.scratch, order.manifest, now); err == nil ||
					!strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
					t.Fatalf("restore accepted incompatible immutable facts for the same correction: %v", err)
				}
				after, err := order.store.Messages(ctx, chat.ID, "", 10)
				if err != nil || len(after) != 1 || !after[0].Edited || after[0].OriginalText != order.body {
					t.Fatalf("merge disturbed the retained logical correction: %+v, %v", after, err)
				}
				if kept, err := order.store.queries.GetOperation(ctx, string(key)); err != nil || kept != receipt {
					t.Fatalf("merge replaced the existing API acceptance: %+v, %v", kept, err)
				}
				if !tc.equivalent {
					if kept, err := order.store.MessageSource(ctx, key); err != nil || !reflect.DeepEqual(kept, raw) || !reflect.DeepEqual(after, before) {
						t.Fatalf("failed merge changed the command's retained revision or projection: %+v, %v", kept, err)
					}
					if _, afterSequence, err := order.store.Snapshot(ctx); err != nil || afterSequence != sequence {
						t.Fatalf("failed merge moved the event cursor: %d → %d, %v", sequence, afterSequence, err)
					}
				}
			}
		})
	}
}
