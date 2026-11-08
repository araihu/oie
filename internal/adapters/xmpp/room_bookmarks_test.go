package xmpp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestSavedRoomAutojoinParsesBothBookmarkFormats(t *testing.T) {
	for _, value := range []string{"true", "1", "false", "0", "", "TRUE", "invalid"} {
		for _, modern := range []bool{true, false} {
			var rooms []savedRoom
			var err error
			if modern {
				payload := fmt.Sprintf(`<pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="urn:xmpp:bookmarks:1"><item id="room@conference.example.test"><conference xmlns="urn:xmpp:bookmarks:1" name="Saved &amp; room" autojoin="%s"><nick>Other client nickname</nick></conference></item></items></pubsub>`, value)
				rooms, err = parseModernSavedRooms(payload)
			} else {
				payload := fmt.Sprintf(`<query xmlns="jabber:iq:private"><storage xmlns="storage:bookmarks"><conference jid="room@conference.example.test" name="Saved &amp; room" autojoin="%s"><nick>Other client nickname</nick></conference></storage></query>`, value)
				rooms, err = parseLegacySavedRooms(payload)
			}
			if err != nil || len(rooms) != 1 || rooms[0].Address != "room@conference.example.test" || rooms[0].Title != "Saved & room" || rooms[0].AutoJoin != (value == "true" || value == "1") {
				t.Fatalf("modern=%v value=%q: %+v %v", modern, value, rooms, err)
			}
		}
	}
}

func TestAutojoinDoesNotAcceptAnotherPubsubNode(t *testing.T) {
	payload := `<pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="other-node"><item id="room@conference.example.test"><conference xmlns="urn:xmpp:bookmarks:1" autojoin="true"/></item></items></pubsub>`
	if rooms, err := parseModernSavedRooms(payload); err == nil || len(rooms) != 0 {
		t.Fatal("non-bookmark pubsub item authorized a room join")
	}
}

type bookmarkHistoryHost struct {
	application.HistoryHost
	bookmark application.RoomBookmark
}

func (h *bookmarkHistoryHost) RestoreRoomBookmark(_ context.Context, bookmark application.RoomBookmark) error {
	h.bookmark = bookmark
	return nil
}

func TestBookmarkJoinNicknameUsesOwnAccountUser(t *testing.T) {
	host := &bookmarkHistoryHost{}
	account := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "guicastro@example.test/desktop"}
	room := savedRoom{Address: "room@conference.example.test", Title: "Saved room", AutoJoin: true}
	if err := restoreSavedRoom(t.Context(), host, account, room); err != nil {
		t.Fatal(err)
	}
	if host.bookmark.Nickname != "guicastro" || host.bookmark.AccountID != account.ID || host.bookmark.Address != room.Address || !host.bookmark.AutoJoin {
		t.Fatalf("bookmark lost account scope/default nickname: %+v", host.bookmark)
	}
	long := domain.Account{Address: strings.Repeat("a", 120) + "@example.test"}
	if len([]rune(defaultRoomNickname(long))) != 100 {
		t.Fatal("default nickname exceeds existing room nickname limit")
	}
}
