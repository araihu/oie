package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBackupRestoreSkipsAncestorOfPromotedRoom(t *testing.T) {
	source, engine, placeholder := outboxFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	dest := copyRestoreFixture(t, source)
	destEngine, room := promoteBackupRoomFixture(t, dest, placeholder)
	changedRoom, err := destEngine.UpdateConversationPreferences(ctx, migrationID(), room.ID, preferenceInput(room, nil, preferenceBool(true)))
	if err != nil {
		t.Fatal(err)
	}
	engine.SetProtocols(&outboundProtocol{})
	other, err := engine.OpenConversation(ctx, migrationID(), placeholder.AccountID, "backup-other@localhost")
	if err != nil {
		t.Fatal(err)
	}
	message := replySeed(t, source, other.Conversation, "history from a different conversation", now, nil, nil)
	manifest, archive, scratch := backupRestoreFixture(t, source, false)

	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{Conversations: 1, Messages: 1}) {
		t.Fatalf("stale placeholder blocked unrelated history restore: %+v, %v", result, err)
	}
	if got, err := dest.Message(ctx, message.ID); err != nil || got.OriginalText != message.OriginalText {
		t.Fatalf("unrelated history was not restored: %+v, %v", got, err)
	}
	got, err := dest.Conversation(ctx, room.ID)
	if err != nil || got.Kind != "room" || got.Title != room.Title || !got.Favorite || got.RevisionID != changedRoom.Conversation.RevisionID {
		t.Fatalf("restore modified the promoted room: %+v, %v", got, err)
	}
	if result, err := dest.mergeBackup(ctx, archive, scratch, manifest, now); err != nil || result != (RestoreSummary{}) {
		t.Fatalf("reapplying the same backup changed the destination: %+v, %v", result, err)
	}
}

func TestBackupRestoreRejectsIncompatibleDirectRoomHistory(t *testing.T) {
	for _, tc := range []struct {
		name         string
		changeSource func(*testing.T, *Store, *application.Engine, domain.Conversation)
	}{
		{"message", func(t *testing.T, source *Store, _ *application.Engine, direct domain.Conversation) {
			replySeed(t, source, direct, "direct history", time.Now().UTC(), nil, nil)
		}},
		{"staged attachment", func(t *testing.T, _ *Store, engine *application.Engine, direct domain.Conversation) {
			if _, _, err := engine.StageAttachment(t.Context(), direct.ID, migrationID(), "direct.txt", "text/plain", strings.NewReader("direct attachment")); err != nil {
				t.Fatal(err)
			}
		}},
		{"concurrent revision", func(t *testing.T, _ *Store, engine *application.Engine, direct domain.Conversation) {
			if _, err := engine.UpdateConversationPreferences(t.Context(), migrationID(), direct.ID, preferenceInput(direct, nil, preferenceBool(true))); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, engine, placeholder := outboxFixture(t)
			dest := copyRestoreFixture(t, source)
			_, room := promoteBackupRoomFixture(t, dest, placeholder)
			tc.changeSource(t, source, engine, placeholder)
			manifest, archive, scratch := backupRestoreFixture(t, source, false)
			if _, err := dest.mergeBackup(t.Context(), archive, scratch, manifest, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "conflicting account, conversation, or message identities") {
				t.Fatalf("incompatible direct and room state was not rejected as a conflict: %v", err)
			}
			got, err := dest.Conversation(t.Context(), room.ID)
			if err != nil || got.Kind != "room" || got.RevisionID != room.RevisionID {
				t.Fatalf("failed restore modified the promoted room: %+v, %v", got, err)
			}
		})
	}
}

func promoteBackupRoomFixture(t *testing.T, store *Store, placeholder domain.Conversation) (*application.Engine, domain.Conversation) {
	t.Helper()
	engine := testRestoreEngine(t, store)
	engine.SetProtocols(&outboundProtocol{})
	promoted, err := engine.OpenRoom(t.Context(), migrationID(), placeholder.AccountID, placeholder.Address, "Tester", "", false)
	if err != nil || promoted.Conversation.ID != placeholder.ID || promoted.Conversation.Kind != "room" {
		t.Fatalf("opening a room did not promote its empty direct placeholder: %+v, %v", promoted, err)
	}
	return engine, promoted.Conversation
}
