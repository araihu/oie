package sqlite

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreBindsNativeCorrectionIDToAuthenticatedRoomAuthority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		sharedKey bool
	}{
		{name: "shared physical ID", sharedKey: true},
		{name: "distinct physical IDs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			source, editor, placeholder := outboxFixture(t)
			room := outboundRoom(t, source, placeholder.AccountID, "group@rooms.localhost")
			other := copyRestoreFixture(t, source)
			firstKey, secondKey := migrationID(), migrationID()
			if tc.sharedKey {
				secondKey = firstKey
			}
			type profile struct {
				store   *Store
				engine  *application.Engine
				key     domain.ID
				nick    string
				root    domain.ID
				body    string
				archive string
				before  []domain.Message
				cursor  int64
			}
			profiles := []*profile{
				{store: source, engine: editor, key: firstKey, nick: "FirstNick"},
				{store: other, engine: testRestoreEngine(t, other), key: secondKey, nick: "SecondNick"},
			}
			for _, side := range profiles {
				author := room.Address + "/" + side.nick
				protocol := &roomEditWitnessProtocol{
					scope: "xmpp:correct:room:" + room.Address + ":" + bodyHash(author+"\x00own-occupant-id"), author: author,
				}
				side.engine.SetProtocols(protocol)
				joined, err := side.engine.JoinRoom(ctx, migrationID(), room.ID, side.nick, "")
				if err != nil {
					t.Fatal(err)
				}
				protocol.intent = joined.Session.IntentRevisionID
				original := ownSubmittedEdit(t, side.store, side.engine, room, side.nick+" original")
				side.root, side.body = original.ID, side.nick+" correction"
				target, err := side.engine.MessageEditTarget(ctx, original.ID)
				if err != nil || !target.Eligible {
					t.Fatalf("edit fixture target: %+v, %v", target, err)
				}
				input, err := json.Marshal(map[string]string{
					"expectedRevisionId": string(target.CurrentRevisionID), "originalText": side.body,
				})
				if err != nil {
					t.Fatal(err)
				}
				handler, err := httpapi.New(side.engine)
				if err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodPost, "/api/v1/messages/"+string(original.ID)+"/edits", bytes.NewReader(input)).WithContext(ctx)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Idempotency-Key", string(side.key))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusAccepted {
					t.Fatalf("edit fixture acceptance: %d %s", response.Code, response.Body.String())
				}
				finishEdit(t, side.store, side.key, "submitted")
				side.before, err = side.store.Messages(ctx, room.ID, "", 10)
				if err != nil || len(side.before) != 1 || side.before[0].ID != side.root ||
					!side.before[0].Edited || side.before[0].OriginalText != side.body {
					t.Fatalf("edit fixture timeline: %+v, %v", side.before, err)
				}
				_, side.cursor, err = side.store.Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := side.store.Close(); err != nil {
					t.Fatal(err)
				}
				var backup bytes.Buffer
				if err := WriteProfileBackup(ctx, side.store.dir, &backup, false); err != nil {
					t.Fatal(err)
				}
				side.archive = filepath.Join(t.TempDir(), "profile.zip")
				if err := os.WriteFile(side.archive, backup.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i, retained := range profiles {
				incoming := profiles[1-i]
				result, err := RestoreProfileBackup(ctx, retained.store.dir, incoming.archive, migrationID())
				if tc.sharedKey {
					if err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
						t.Fatalf("restore %s into %s accepted a shared physical ID for different authenticated corrections: %+v, %v", incoming.nick, retained.nick, result, err)
					}
				} else if err != nil || result.Messages != 1 {
					t.Fatalf("restore %s into %s rejected distinct authenticated corrections: %+v, %v", incoming.nick, retained.nick, result, err)
				}
				store, err := Open(ctx, retained.store.dir, migrationID())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				timeline, err := store.Messages(ctx, room.ID, "", 10)
				if err != nil {
					t.Fatal(err)
				}
				if tc.sharedKey {
					if _, after, err := store.Snapshot(ctx); err != nil || after != retained.cursor || !reflect.DeepEqual(timeline, retained.before) {
						t.Fatalf("failed restore changed retained correction history: cursor %d before %d; %v", after, retained.cursor, err)
					}
					continue
				}
				got := make(map[domain.ID]string)
				for _, message := range timeline {
					if !message.Edited || message.ProjectionSourceID == "" {
						t.Fatalf("lost correction projection: %+v", message)
					}
					got[message.ID] = message.OriginalText
				}
				want := map[domain.ID]string{retained.root: retained.body, incoming.root: incoming.body}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("distinct authenticated roots: got %v, want %v", got, want)
				}
			}
		})
	}
}
