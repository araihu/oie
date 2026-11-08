package httpapi_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestBookmarkAutojoinPreservesLocalChoicesAndRestarts(t *testing.T) {
	p := &roomHTTPProtocol{protocolStub: &protocolStub{connected: true}, rooms: map[domain.ID]domain.RoomSession{}}
	e, store, _ := newMessagingEngine(t, t.TempDir(), p)
	p.host = e
	c := openTestConversation(t, e, "alice@localhost")
	bookmark := application.RoomBookmark{AccountID: c.AccountID, Address: "saved@rooms.localhost", Title: "Saved room", Nickname: "alice"}
	if err := e.RestoreRoomBookmark(t.Context(), bookmark); err != nil {
		t.Fatal(err)
	}
	room, err := store.RestoredRoom(t.Context(), c.AccountID, bookmark.Address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RoomIntent(t.Context(), room.ID); !errors.Is(err, application.ErrNotFound) {
		t.Fatal("bookmark without autojoin created an intent", err)
	}
	bookmark.AutoJoin = true
	errorsFound := make(chan error, 8)
	var group sync.WaitGroup
	for i := 0; i < cap(errorsFound); i++ {
		group.Add(1)
		go func() { defer group.Done(); errorsFound <- e.RestoreRoomBookmark(t.Context(), bookmark) }()
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	intent, err := store.RoomIntent(t.Context(), room.ID)
	if err != nil || !intent.Active || intent.Nickname != "alice" || intent.IntentRevisionID == "" {
		t.Fatalf("missing durable account-user join intent: %+v %v", intent, err)
	}
	before, _ := e.Snapshot(t.Context())
	if err = e.RestoreRoomBookmark(t.Context(), bookmark); err != nil {
		t.Fatal(err)
	}
	after, _ := e.Snapshot(t.Context())
	if before.Sequence != after.Sequence {
		t.Fatal("bookmark replay generated a new intent/event")
	}
	stop := runWorker(t, e)
	deadline := time.Now().Add(3 * time.Second)
	joined := false
	for time.Now().Before(deadline) {
		session, readErr := e.RoomSession(t.Context(), room.ID)
		if readErr == nil && session.State == "joined" && session.Nickname == "alice" {
			joined = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if !joined {
		t.Fatal("autojoin intent did not drive a protocol join with account username")
	}
	left, err := e.LeaveRoom(t.Context(), id(), room.ID, intent.IntentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := application.New(store, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetProtocols(p)
	if err = restarted.RestoreRoomBookmark(t.Context(), bookmark); err != nil {
		t.Fatal(err)
	}
	retained, err := store.RoomIntent(t.Context(), room.ID)
	if err != nil || retained.Active || retained.IntentRevisionID != left.Session.IntentRevisionID {
		t.Fatalf("bookmark autojoin revived explicit leave after restart: %+v %v", retained, err)
	}
}

func TestBookmarkAutojoinDoesNotReplaceNicknameOrRevokedIntent(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		p := &roomHTTPProtocol{protocolStub: &protocolStub{connected: true}, rooms: map[domain.ID]domain.RoomSession{}}
		e, store, _ := newMessagingEngine(t, t.TempDir(), p)
		p.host = e
		c := openTestConversation(t, e, "alice@localhost")
		bookmark := application.RoomBookmark{AccountID: c.AccountID, Address: "saved@rooms.localhost", Title: "Saved room", Nickname: "alice"}
		if err := e.RestoreRoomBookmark(t.Context(), bookmark); err != nil {
			t.Fatal(err)
		}
		room, err := store.RestoredRoom(t.Context(), c.AccountID, bookmark.Address)
		if err != nil {
			t.Fatal(err)
		}
		manual, err := e.JoinRoom(t.Context(), id(), room.ID, "Explicit nickname", "")
		if err != nil {
			t.Fatal(err)
		}
		if revoked {
			if err = e.RoomSessionChanged(t.Context(), domain.RoomSession{AccountID: c.AccountID, ConversationID: room.ID, IntentRevisionID: manual.Session.IntentRevisionID, State: "failed", Revoked: true, LastErrorCode: "removed"}); err != nil {
				t.Fatal(err)
			}
		}
		before, _ := store.RoomIntent(t.Context(), room.ID)
		bookmark.AutoJoin = true
		if err = e.RestoreRoomBookmark(t.Context(), bookmark); err != nil {
			t.Fatal(err)
		}
		after, err := store.RoomIntent(t.Context(), room.ID)
		if err != nil || before.IntentRevisionID != after.IntentRevisionID || before.Active != after.Active || before.Nickname != after.Nickname {
			t.Fatalf("bookmark replaced explicit choice/revocation: %+v %+v %v", before, after, err)
		}
	}
}
