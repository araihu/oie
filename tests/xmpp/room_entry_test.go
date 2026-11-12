package xmpp_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
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
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
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
	room, err := jid.Parse("entry-" + string(id()) + "@conference.localhost")
	if err != nil {
		t.Fatal(err)
	}
	address := room.String()
	preview, err := engine.PreviewRoom(ctx, bob, address)
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
	failed := waitRoom(absent.Conversation.ID, "failed")
	if failed.LastErrorCode != "room_unavailable" {
		t.Fatalf("wrong failure: %+v", failed)
	}
	if _, err := engine.LeaveRoom(ctx, id(), absent.Conversation.ID, failed.IntentRevisionID); err != nil {
		t.Fatal(err)
	}
	preview, err = engine.PreviewRoom(ctx, bob, address)
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
	preview, err = engine.PreviewRoom(ctx, bob, address)
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
	preview, err = engine.PreviewRoom(ctx, bob, address)
	if err != nil || preview.State != "known" || preview.Name != "Garden vCard" || preview.Description != "Room profile" || preview.AvatarHash == "" {
		t.Fatalf("room vCard: %+v %v", preview, err)
	}
	if _, err := engine.AvatarImage(ctx, preview.AvatarHash); err != nil {
		t.Fatal(err)
	}
	if current, err := engine.RoomSession(ctx, absent.Conversation.ID); err != nil || current.Active || current.State == "joined" {
		t.Fatalf("preview changed membership: %+v %v", current, err)
	}
	joinedBob, err := engine.OpenRoom(ctx, id(), bob, address, "Reader", "", false)
	if err != nil || joinedBob.Conversation.ID != absent.Conversation.ID {
		t.Fatalf("entry did not reuse room: %+v %v", joinedBob, err)
	}
	waitRoom(joinedBob.Conversation.ID, "joined")
	if _, err := engine.Disconnect(ctx, alice); err != nil {
		t.Fatal(err)
	}
	waitRoom(created.Conversation.ID, "not_joined")
	if _, err := engine.ConfigureConnection(ctx, alice, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
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
	if _, err := engine.OpenRoom(ctx, id(), alice, address, "Host", "Replacement", true); err != nil {
		t.Fatal(err)
	}
	if collision := waitRoom(created.Conversation.ID, "failed"); collision.LastErrorCode != "room_exists" || collision.Active {
		t.Fatalf("existing room was not rejected: %+v", collision)
	}
	preview, err = engine.PreviewRoom(ctx, bob, address)
	if err != nil || preview.Name != "Garden vCard" {
		t.Fatalf("creation collision changed room: %+v %v", preview, err)
	}
}
