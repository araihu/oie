package xmpp_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/avatarfiles"
	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestRoomEntryCreationPreviewAndReplay(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("development Prosody required")
	}
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("fixture CA")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "hosted", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetAvatarFiles(avatarfiles.New(filepath.Join(dir, "avatars")))
	manager := adapter.New(filepath.Join(dir, "connections"), roots)
	engine.SetProtocols(manager)
	if err := manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("room worker: %v", err)
		}
	}()
	register := func(address, password string) domain.ID {
		t.Helper()
		reg, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: address, Label: address})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := engine.ConfigureConnection(ctx, reg.Account.ID, application.ConnectionInput{Password: password, Endpoint: xmppEndpoint()}); err != nil {
			t.Fatal(err)
		}
		eventually(t, func() bool {
			c, err := engine.Connection(ctx, reg.Account.ID)
			return err == nil && c.State == "connected"
		})
		return reg.Account.ID
	}
	alice := register("alice@localhost", "alice-local-only")
	bob := register("bob@localhost", "bob-local-only")
	invalidKey := id()
	if _, err := engine.OpenRoom(ctx, invalidKey, alice, "wrong@localhost", "", "Wrong domain", true); !errors.Is(err, application.ErrInvalidRoomService) {
		t.Fatalf("account domain accepted as a room service: %v", err)
	}
	if _, err := engine.Operation(ctx, invalidKey); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("bad service left an accepted operation: %v", err)
	}
	if chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: alice, Kind: "room", Limit: 10}); err != nil || len(chats) != 0 {
		t.Fatalf("bad service created a local chat: %+v %v", chats, err)
	}
	room, err := jid.Parse("entry-" + string(id()) + "@conference.localhost")
	if err != nil {
		t.Fatal(err)
	}
	address := room.String()
	preview, err := engine.PreviewRoom(ctx, bob, address, false)
	if err != nil || preview.State != "not_found" {
		t.Fatalf("preview absent room: %+v %v", preview, err)
	}
	rows, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: bob, Kind: "room", Limit: 10})
	if err != nil || len(rows) != 0 {
		t.Fatalf("preview saved a room: %+v %v", rows, err)
	}
	absent, err := engine.OpenRoom(ctx, id(), bob, address, "Visitor", "", false)
	if err != nil {
		t.Fatal(err)
	}
	waitRoom := func(conversation domain.ID, state string) domain.RoomSession {
		t.Helper()
		var latest domain.RoomSession
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			var err error
			latest, err = engine.RoomSession(ctx, conversation)
			if err != nil {
				t.Fatal(err)
			}
			if latest.State == state {
				return latest
			}
			if state == "joined" && latest.State == "failed" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("room %s: wanted %s, got %+v", conversation, state, latest)
		return latest
	}
	waitListStatus := func(account, conversation domain.ID, status string) {
		t.Helper()
		eventually(t, func() bool {
			chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: account, Kind: "room", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			return slices.ContainsFunc(chats, func(c domain.Conversation) bool {
				return c.ID == conversation && c.RoomSession != nil && c.RoomSession.DisplayStatus() == status
			})
		})
	}
	failed := waitRoom(absent.Conversation.ID, "failed")
	waitListStatus(bob, absent.Conversation.ID, "unavailable")
	if failed.LastErrorCode != "room_not_found" {
		t.Fatalf("wrong failure: %+v", failed)
	}
	if failed.Active {
		eventually(t, func() bool {
			session, err := engine.RoomSession(ctx, absent.Conversation.ID)
			return err == nil && !session.Active && session.LastErrorCode == "room_not_found"
		})
	}
	preview, err = engine.PreviewRoom(ctx, bob, address, false)
	if err != nil || preview.State != "not_found" {
		t.Fatalf("entry created a room: %+v %v", preview, err)
	}
	createKey := id()
	created, err := engine.OpenRoom(ctx, createKey, alice, address, "Host", "Garden & Books", true)
	if err != nil {
		t.Fatal(err)
	}
	publisher, raw := seedSession(t, ctx, "alice@localhost", "alice-local-only", roots)
	defer func() { _ = raw.Close() }()
	go func() { _ = publisher.Serve(nil) }()
	ownerIQ := func(payload string) error {
		response, err := publisher.SendIQ(ctx, stanza.IQ{To: room, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(payload))))
		if err != nil {
			return err
		}
		defer func() { _ = response.Close() }()
		var result stanza.IQ
		if err := xml.NewTokenDecoder(response).Decode(&result); err != nil {
			return err
		}
		if result.Type != stanza.ResultIQ {
			return fmt.Errorf("unexpected room IQ response: %s", result.Type)
		}
		return nil
	}
	defer func() {
		_ = ownerIQ(`<query xmlns="http://jabber.org/protocol/muc#owner"><destroy/></query>`)
	}()
	joined := waitRoom(created.Conversation.ID, "joined")
	if joined.Nickname != "Host" {
		t.Fatalf("unexpected creator nickname: %+v", joined)
	}
	preview, err = engine.PreviewRoom(ctx, bob, address, false)
	if err != nil || preview.State != "known" || preview.Name != "Garden & Books" || preview.AvatarHash != "" {
		t.Fatalf("disco fallback before vCard: %+v %v", preview, err)
	}
	imageData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.RGBA{R: 255, A: 255})
	var avatar bytes.Buffer
	if err := png.Encode(&avatar, imageData); err != nil {
		t.Fatal(err)
	}
	if err := ownerIQ(`<vCard xmlns="vcard-temp"><FN>Garden vCard</FN><DESC>Room profile</DESC><PHOTO><TYPE>image/png</TYPE><BINVAL>` + base64.StdEncoding.EncodeToString(avatar.Bytes()) + `</BINVAL></PHOTO></vCard>`); err != nil {
		t.Fatal("publish room vCard:", err)
	}
	preview, err = engine.PreviewRoom(ctx, bob, address, false)
	if err != nil || preview.State != "known" || preview.Name != "Garden vCard" || preview.Description != "Room profile" || preview.AvatarHash == "" {
		t.Fatalf("room vCard: %+v %v", preview, err)
	}
	if _, err := engine.AvatarImage(ctx, preview.AvatarHash); err != nil {
		t.Fatal(err)
	}
	if current, err := engine.RoomSession(ctx, absent.Conversation.ID); err != nil || current.Active || current.State == "joined" {
		t.Fatalf("preview changed membership: %+v %v", current, err)
	}
	if err := ownerIQ(`<query xmlns="http://jabber.org/protocol/muc#owner"><x xmlns="jabber:x:data" type="submit"><field var="FORM_TYPE"><value>http://jabber.org/protocol/muc#roomconfig</value></field><field var="muc#roomconfig_passwordprotectedroom"><value>1</value></field><field var="muc#roomconfig_roomsecret"><value>fixture password</value></field></x></query>`); err != nil {
		t.Fatal("configure password:", err)
	}
	joinedBob, err := engine.OpenRoom(ctx, id(), bob, address, "Reader", "", false, "fixture password")
	if err != nil || joinedBob.Conversation.ID != absent.Conversation.ID {
		t.Fatalf("entry did not reuse room: %+v %v", joinedBob, err)
	}
	waitRoom(joinedBob.Conversation.ID, "joined")
	if err := engine.StoreAvatar(ctx, bob, "bob@localhost", "room-roster-fixture", avatar.Bytes()); err != nil {
		t.Fatal(err)
	}
	readerAvatar, err := engine.Avatar(ctx, bob, "bob@localhost")
	if err != nil || readerAvatar.ImageHash == "" {
		t.Fatalf("cache reader avatar: %+v %v", readerAvatar, err)
	}
	eventually(t, func() bool {
		roster, err := engine.RoomParticipants(ctx, joinedBob.Conversation.ID)
		return err == nil && roster.State == "available" && !roster.Truncated && len(roster.Participants) == 2 &&
			slices.ContainsFunc(roster.Participants, func(p domain.RoomSessionParticipant) bool {
				return p.Self && p.Nickname == "Reader" && p.Role == "participant" && p.AvatarHash == readerAvatar.ImageHash
			}) && slices.ContainsFunc(roster.Participants, func(p domain.RoomSessionParticipant) bool {
			return !p.Self && p.Nickname == "Host" && p.Affiliation == "owner" && p.AvatarHash == ""
		})
	})
	directory, err := engine.DiscoverRooms(ctx, bob, "conference.localhost")
	if err != nil || !slices.ContainsFunc(directory.Rooms, func(item domain.RoomDirectoryItem) bool { return item.Address == address }) {
		t.Fatalf("public room missing: %+v %v", directory, err)
	}
	manage := func(accountRoom domain.ID, action, target, value, want string) {
		t.Helper()
		info, err := engine.RoomManagement(ctx, accountRoom, "")
		if err != nil || info.Revision == "" {
			t.Fatalf("management snapshot: %+v %v", info, err)
		}
		key := id()
		cmd := domain.RoomManagementCommand{Action: action, Target: target, Value: value, ExpectedRevision: info.Revision}
		for range 2 {
			state, err := engine.ManageRoom(ctx, key, accountRoom, cmd)
			if err != nil || state != want {
				t.Fatalf("%s: wanted %s, got %s %v (%+v)", action, want, state, err, info)
			}
		}
	}
	imageData.Set(0, 0, color.RGBA{B: 255, A: 255})
	avatar.Reset()
	if err := png.Encode(&avatar, imageData); err != nil {
		t.Fatal(err)
	}
	publishAvatar := func(room domain.ID, action string, image []byte, want string) {
		t.Helper()
		info, err := engine.RoomManagement(ctx, room, "")
		if err != nil || info.Revision == "" || slices.Contains(info.Actions, action) != (want == "confirmed") {
			t.Fatalf("avatar permissions: %+v %v", info, err)
		}
		key := id()
		cmd := domain.RoomManagementCommand{Action: action, Image: image, ExpectedRevision: info.Revision}
		for range 2 {
			if state, err := engine.ManageRoom(ctx, key, room, cmd); err != nil || state != want {
				t.Fatalf("%s: wanted %s, got %s %v", action, want, state, err)
			}
		}
	}
	publishAvatar(joinedBob.Conversation.ID, "set_avatar", avatar.Bytes(), "denied")
	publishAvatar(created.Conversation.ID, "set_avatar", avatar.Bytes(), "confirmed")
	replaced, err := engine.PreviewRoom(ctx, bob, address, false)
	if err != nil || replaced.Name != preview.Name || replaced.Description != preview.Description ||
		replaced.AvatarHash == "" || replaced.AvatarHash == preview.AvatarHash {
		t.Fatalf("replace photo only: %+v %v", replaced, err)
	}
	eventually(t, func() bool {
		cached, err := engine.Avatar(ctx, bob, address)
		return err == nil && cached.ImageHash == replaced.AvatarHash
	})
	publishAvatar(created.Conversation.ID, "remove_avatar", nil, "confirmed")
	removed, err := engine.PreviewRoom(ctx, bob, address, false)
	if err != nil || removed.AvatarHash != "" || removed.Name != preview.Name || removed.Description != preview.Description {
		t.Fatalf("remove photo only: %+v %v", removed, err)
	}
	eventually(t, func() bool {
		cached, err := engine.Avatar(ctx, bob, address)
		return err == nil && cached.ImageHash == ""
	})
	manage(joinedBob.Conversation.ID, "rename", "", "Forged", "denied")
	name, topic := "Renamed Garden", "Reading plans\r\nBring a book"
	for _, check := range []struct {
		room domain.ID
		want string
	}{{joinedBob.Conversation.ID, "denied"}, {created.Conversation.ID, "submitted"}} {
		info, err := engine.RoomManagement(ctx, check.room, "")
		if err != nil || info.Revision == "" {
			t.Fatalf("room details permissions: %+v %v", info, err)
		}
		key := id()
		cmd := domain.RoomManagementCommand{Action: "update_details", ExpectedRevision: info.Revision, Name: &name, Topic: &topic}
		for range 2 {
			state, err := engine.ManageRoom(ctx, key, check.room, cmd)
			if err != nil || state != check.want {
				t.Fatalf("combined room update: wanted %s, got %s %v", check.want, state, err)
			}
		}
	}
	manage(created.Conversation.ID, "invite", "bob@localhost", "", "submitted")
	if os.Getenv("OIE_XMPP_MODERATION_INTEGRATION") == "1" {
		info, err := engine.RoomManagement(ctx, created.Conversation.ID, "")
		if err != nil || !slices.Contains(info.Actions, "moderate_message") {
			t.Fatalf("moderation not advertised: %+v %v", info, err)
		}
		sent, err := engine.SendMessage(ctx, id(), joinedBob.Conversation.ID, "Moderation fixture")
		if err != nil {
			t.Fatal(err)
		}
		var target domain.ID
		eventually(t, func() bool {
			items, err := engine.Messages(ctx, created.Conversation.ID, "", 20)
			if err != nil {
				return false
			}
			for _, item := range items {
				if item.OriginalText != sent.Message.OriginalText {
					continue
				}
				check, err := engine.RoomManagementForMessage(ctx, created.Conversation.ID, "", item.ID)
				if err == nil && slices.Contains(check.Actions, "moderate_message") {
					info, target = check, item.ID
					return true
				}
			}
			return false
		})
		state, err := engine.ManageRoom(ctx, id(), created.Conversation.ID, domain.RoomManagementCommand{
			Action: "moderate_message", ExpectedRevision: info.Revision, MessageID: target, Reason: "Room rules",
		})
		if err != nil || state != "confirmed" {
			t.Fatalf("moderation: %s %v", state, err)
		}
		eventually(t, func() bool {
			item, err := engine.Message(ctx, target)
			return err == nil && item.Moderation != nil && item.OriginalText == "" && item.Moderation.Reason == "Room rules"
		})
	}

	eventually(t, func() bool {
		info, err := engine.ConversationDetails(ctx, created.Conversation.ID)
		return err == nil && info.Name == "Renamed Garden" && info.Topic == "Reading plans\nBring a book"
	})
	var reader string
	eventually(t, func() bool {
		info, err := engine.RoomManagement(ctx, created.Conversation.ID, "")
		for _, occupant := range info.Occupants {
			if occupant.Nickname == "Reader" {
				reader = occupant.ID
			}
		}
		return err == nil && reader != ""
	})
	manage(created.Conversation.ID, "role", reader, "none", "confirmed")
	kicked := waitRoom(joinedBob.Conversation.ID, "failed")
	if kicked.LastErrorCode != "removed" || kicked.Active {
		t.Fatalf("kick must revoke rejoin: %+v", kicked)
	}
	if _, err := engine.OpenRoom(ctx, id(), bob, address, "Reader", "", false); err != nil {
		t.Fatal(err)
	}
	waitRoom(joinedBob.Conversation.ID, "joined")
	manage(created.Conversation.ID, "affiliation", "bob@localhost", "member", "confirmed")
	var rejoinedReader string
	eventually(t, func() bool {
		info, err := engine.RoomManagement(ctx, created.Conversation.ID, "")
		for _, occupant := range info.Occupants {
			if occupant.Nickname == "Reader" && occupant.ID != reader && occupant.Affiliation == "member" && slices.Contains(occupant.Affiliations, "outcast") {
				rejoinedReader = occupant.ID
			}
		}
		return err == nil && rejoinedReader != ""
	})
	manage(created.Conversation.ID, "affiliation", rejoinedReader, "outcast", "confirmed")
	bans, err := engine.RoomManagement(ctx, created.Conversation.ID, "outcast")
	if err != nil || !slices.Contains(bans.Affiliations, "bob@localhost") || bans.BannedNicknames["bob@localhost"] != "Reader" {
		t.Fatalf("ban list: %+v %v", bans, err)
	}
	manage(created.Conversation.ID, "affiliation", "bob@localhost", "none", "confirmed")
	names, err := store.RoomBannedNicknames(ctx, created.Conversation.ID)
	if err != nil || len(names) != 0 {
		t.Fatalf("nickname after lifting ban: %+v %v", names, err)
	}
	if _, err := engine.OpenRoom(ctx, id(), bob, address, "Reader", "", false); err != nil {
		t.Fatal(err)
	}
	waitRoom(joinedBob.Conversation.ID, "joined")
	waitListStatus(bob, joinedBob.Conversation.ID, "joined")
	if _, err := engine.Disconnect(ctx, bob); err != nil {
		t.Fatal(err)
	}
	waitRoom(joinedBob.Conversation.ID, "not_joined")
	waitListStatus(bob, joinedBob.Conversation.ID, "disconnected")
	if _, err := engine.ConfigureConnection(ctx, bob, application.ConnectionInput{Password: "bob-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	waitRoom(joinedBob.Conversation.ID, "joined")
	if _, err := engine.Disconnect(ctx, alice); err != nil {
		t.Fatal(err)
	}
	waitRoom(created.Conversation.ID, "not_joined")
	if roster, err := engine.RoomParticipants(ctx, created.Conversation.ID); err != nil || roster.State != "not_joined" || len(roster.Participants) != 0 {
		t.Fatalf("roster after leaving: %+v %v", roster, err)
	}
	if _, err := engine.ConfigureConnection(ctx, alice, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
		t.Fatal(err)
	}
	// The creator was already present when the room password was set.
	eventually(t, func() bool {
		session, err := engine.RoomSession(ctx, created.Conversation.ID)
		return err == nil && !session.Active && session.LastErrorCode == "not_authorized"
	})
	if _, err := engine.OpenRoom(ctx, id(), alice, address, "Host", "", false, "fixture password"); err != nil {
		t.Fatal(err)
	}
	joined = waitRoom(created.Conversation.ID, "joined")
	if _, err := engine.LeaveRoom(ctx, id(), created.Conversation.ID, joined.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	waitRoom(created.Conversation.ID, "not_joined")
	replayed, err := engine.OpenRoom(ctx, createKey, alice, address, "Host", "Garden & Books", true)
	if err != nil || !replayed.Replayed || replayed.Conversation.ID != created.Conversation.ID {
		t.Fatalf("creation receipt: %+v %v", replayed, err)
	}
	current, err := engine.RoomSession(ctx, created.Conversation.ID)
	if err != nil || current.Active {
		t.Fatalf("replay reactivated leave: %+v %v", current, err)
	}
	if _, err := engine.OpenRoom(ctx, id(), alice, address, "Host", "Replacement", true); !errors.Is(err, application.ErrRoomAlreadyExists) {
		t.Fatalf("existing room was not rejected before saving: %v", err)
	}
	if current, err := engine.RoomSession(ctx, created.Conversation.ID); err != nil || current.Active {
		t.Fatalf("creation collision reactivated leave: %+v %v", current, err)
	}
	preview, err = engine.PreviewRoom(ctx, bob, address, false)
	if err != nil || preview.Name != "Garden vCard" {
		t.Fatalf("creation collision changed room: %+v %v", preview, err)
	}

	manage(joinedBob.Conversation.ID, "destroy", "", "", "denied")
	testRoomRemoval(t, ctx, engine, joinedBob.Conversation.ID, bob, address)
	waitRoom(joinedBob.Conversation.ID, "joined")
	if _, err := engine.OpenRoom(ctx, id(), alice, address, "Host", "", false); err != nil {
		t.Fatal(err)
	}
	waitRoom(created.Conversation.ID, "joined")
	manage(created.Conversation.ID, "destroy", "", "", "confirmed")
	for _, conversation := range []domain.ID{created.Conversation.ID, joinedBob.Conversation.ID} {
		eventually(t, func() bool {
			session, err := engine.RoomSession(ctx, conversation)
			return err == nil && !session.Active && session.LastErrorCode == "room_destroyed"
		})
	}
	stored, err := engine.Conversation(ctx, created.Conversation.ID)
	if err != nil || !stored.RemovedFromChats {
		t.Fatalf("confirmed destruction should retain and hide the owner's conversation: %+v %v", stored, err)
	}
	for _, archived := range []bool{false, true} {
		chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: alice, Kind: "room", Limit: 10, Archived: archived})
		if err != nil || slices.ContainsFunc(chats, func(c domain.Conversation) bool { return c.ID == created.Conversation.ID }) {
			t.Fatalf("destroyed room in owner's list (archived=%v): %+v %v", archived, chats, err)
		}
	}
	participant, err := engine.Conversation(ctx, joinedBob.Conversation.ID)
	if err != nil || participant.RemovedFromChats {
		t.Fatalf("destruction notice should preserve the other participant's chat: %+v %v", participant, err)
	}

	waitListStatus(bob, joinedBob.Conversation.ID, "destroyed")
	if preview, err = engine.PreviewRoom(ctx, bob, address, false); err != nil || preview.State == "known" {
		t.Fatalf("server room still answers discovery after confirmed deletion: %+v %v", preview, err)
	}

	roomsAfterDeletion, err := engine.DiscoverRooms(ctx, bob, "conference.localhost")
	if err != nil || roomsAfterDeletion.State != "known" || slices.ContainsFunc(roomsAfterDeletion.Rooms, func(item domain.RoomDirectoryItem) bool { return item.Address == address }) {
		t.Fatalf("deleted room still in server directory: %+v %v", roomsAfterDeletion, err)
	}

	invitedAddress := "invited-" + string(id()) + "@conference.localhost"
	invitedRoom, err := engine.OpenRoom(ctx, id(), alice, invitedAddress, "Host", "Invitation fixture", true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		target, _ := jid.Parse(invitedAddress)
		response, err := publisher.SendIQ(ctx, stanza.IQ{To: target, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(`<query xmlns="http://jabber.org/protocol/muc#owner"><destroy/></query>`))))
		if err == nil {
			_ = response.Close()
		}
	}()
	waitRoom(invitedRoom.Conversation.ID, "joined")
	manage(invitedRoom.Conversation.ID, "invite", "bob@localhost", "", "submitted")
	var invitation domain.Conversation
	eventually(t, func() bool {
		chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: bob, Kind: "room", Limit: 10})
		if err != nil {
			return false
		}
		for _, chat := range chats {
			if chat.Address == invitedAddress && chat.Request == domain.RoomInvitation {
				invitation = chat
				return true
			}
		}
		return false
	})
	invitedSession := waitRoom(invitation.ID, "not_joined")
	if invitedSession.Active {
		t.Fatalf("invitation implicitly joined: %+v", invitedSession)
	}
	archived := true
	decision, err := engine.UpdateConversationPreferences(ctx, id(), invitation.ID, application.ConversationPreferencesInput{
		AccountID: bob, ExpectedRevisionID: invitation.RevisionID, Archived: &archived,
	})
	if err != nil || !decision.Conversation.Archived || decision.Conversation.Request != domain.RoomInvitation {
		t.Fatalf("archive invitation: %+v %v", decision, err)
	}
	if _, err := engine.JoinRoomInvitation(ctx, id(), invitation.ID, "Reader", invitedSession.IntentRevisionID, decision.Conversation.RevisionID); err != nil {
		t.Fatal(err)
	}
	waitRoom(invitation.ID, "joined")
	eventually(t, func() bool {
		chat, err := engine.Conversation(ctx, invitation.ID)
		return err == nil && chat.Request == "" && chat.Archived
	})
}

func testRoomRemoval(t *testing.T, ctx context.Context, engine *application.Engine, conversation, account domain.ID, address string) {
	t.Helper()
	before, err := engine.Conversation(ctx, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.UpdateConversationPreferences(ctx, id(), conversation, application.ConversationPreferencesInput{
		AccountID: account, ExpectedRevisionID: before.RevisionID, Archived: new(true),
	}); err != nil {
		t.Fatal(err)
	}
	archived, err := engine.Conversation(ctx, conversation)
	if err != nil || !archived.Archived {
		t.Fatalf("archive fixture: %+v %v", archived, err)
	}
	if err := engine.RemoveRoomFromChats(ctx, id(), conversation, account, before.RevisionID); !errors.Is(err, application.ErrStateConflict) {
		t.Fatalf("stale room removal: %v", err)
	}
	key := id()
	if err := engine.RemoveRoomFromChats(ctx, key, conversation, account, archived.RevisionID); err != nil {
		t.Fatal(err)
	}
	for _, onlyArchived := range []bool{false, true} {
		chats, err := engine.Conversations(ctx, "", application.ConversationQuery{AccountID: account, Kind: "room", Limit: 10, Archived: onlyArchived})
		if err != nil || slices.ContainsFunc(chats, func(c domain.Conversation) bool { return c.ID == conversation }) {
			t.Fatalf("removed room still listed (archived=%v): %+v %v", onlyArchived, chats, err)
		}
	}
	eventually(t, func() bool {
		session, err := engine.RoomSession(ctx, conversation)
		return err == nil && !session.Active && session.State == "not_joined"
	})
	if preview, err := engine.PreviewRoom(ctx, account, address, false); err != nil || preview.State != "known" {
		t.Fatalf("local removal changed server room: %+v %v", preview, err)
	}
	reopened, err := engine.OpenRoom(ctx, id(), account, address, "Reader", "", false)
	if err != nil || reopened.Conversation.ID != conversation || reopened.Conversation.RemovedFromChats || reopened.Conversation.Archived {
		t.Fatalf("explicit entry did not restore the room: %+v %v", reopened, err)
	}
	if err := engine.RemoveRoomFromChats(ctx, key, conversation, account, archived.RevisionID); err != nil {
		t.Fatalf("replay of local removal: %v", err)
	}
	if current, err := engine.Conversation(ctx, conversation); err != nil || current.RemovedFromChats {
		t.Fatalf("old removal hid a reopened chat: %+v %v", current, err)
	}
}
