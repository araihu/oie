package xmpp_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/avatarfiles"
	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestPublishedAvatarsUpdateClearAndReconnect(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("development Prosody required")
	}
	roots := x509.NewCertPool()
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		t.Fatal("fixture CA", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	publisher, raw := seedSession(t, ctx, "alice@localhost", "alice-local-only", roots)
	defer func() { _ = raw.Close() }()
	go func() {
		_ = publisher.Serve(mx.HandlerFunc(func(xmlstream.TokenReadEncoder, *xml.StartElement) error { return nil }))
	}()
	own, _ := jid.Parse("alice@localhost")
	publish := func(payload string) {
		t.Helper()
		result, err := publisher.SendIQ(ctx, stanza.IQ{To: own, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(payload))))
		if err != nil {
			t.Fatal(err)
		}
		var iq stanza.IQ
		err = xml.NewTokenDecoder(result).Decode(&iq)
		_ = result.Close()
		if err != nil || iq.Type != stanza.ResultIQ {
			t.Fatalf("avatar fixture publication: %v %v", iq.Type, err)
		}
	}
	metadata := func(hash, body string) {
		publish(`<pubsub xmlns="http://jabber.org/protocol/pubsub"><publish node="urn:xmpp:avatar:metadata"><item id="` + hash + `"><metadata xmlns="urn:xmpp:avatar:metadata">` + body + `</metadata></item></publish></pubsub>`)
	}
	set := func(c color.NRGBA) string {
		t.Helper()
		img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
		img.SetNRGBA(0, 0, c)
		var b bytes.Buffer
		if err := png.Encode(&b, img); err != nil {
			t.Fatal(err)
		}
		sum := sha1.Sum(b.Bytes())
		hash := hex.EncodeToString(sum[:])
		publish(`<pubsub xmlns="http://jabber.org/protocol/pubsub"><publish node="urn:xmpp:avatar:data"><item id="` + hash + `"><data xmlns="urn:xmpp:avatar:data">` + base64.StdEncoding.EncodeToString(b.Bytes()) + `</data></item></publish></pubsub>`)
		metadata(hash, `<info id="`+hash+`" type="image/png"/>`)
		return hash
	}
	firstSource := set(color.NRGBA{R: 255, A: 255})
	// The accounts belong only to the disposable development server.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, node := range []string{"urn:xmpp:avatar:metadata", "urn:xmpp:avatar:data"} {
			r, err := publisher.SendIQ(cleanup, stanza.IQ{To: own, Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(`<pubsub xmlns="http://jabber.org/protocol/pubsub#owner"><delete node="`+node+`"/></pubsub>`))))
			if err == nil {
				_, _ = xmlstream.Copy(xmlstream.Discard(), r)
				_ = r.Close()
			}
		}
	}()
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
	engine.SetAvatarFiles(avatarfiles.New(filepath.Join(dir, "media", "avatars")))
	manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	account, err := engine.Register(ctx, id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Avatar fixture"})
	if err != nil {
		t.Fatal(err)
	}
	connect := func() {
		t.Helper()
		if _, err := engine.ConfigureConnection(ctx, account.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: xmppEndpoint()}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(source string) domain.Avatar {
		t.Helper()
		for ctx.Err() == nil {
			a, err := engine.Avatar(ctx, account.Account.ID, "alice@localhost")
			if err == nil && a.SourceToken == source {
				return a
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("avatar recovery timed out for fixture source", source)
		return domain.Avatar{}
	}
	connect()
	t.Log("connected fixture, waiting initial avatar")
	first := wait("pep:" + firstSource)
	if first.ImageHash == "" {
		t.Fatal("published image missing")
	}
	if _, err = engine.AvatarImage(ctx, first.ImageHash); err != nil {
		t.Fatal(err)
	}
	t.Log("initial avatar recovered")
	time.Sleep(time.Second)
	nextSource := set(color.NRGBA{B: 255, A: 255})
	next := wait("pep:" + nextSource)
	if next.ID != first.ID || next.RevisionID == first.RevisionID || next.ImageHash == first.ImageHash {
		t.Fatal("live avatar update failed")
	}
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	upload := func(data []byte, want int) {
		t.Helper()
		response, err := client.SetAccountAvatarWithResponse(ctx, string(account.Account.ID), engineapi.SetAccountAvatar{Image: data})
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode() != want {
			t.Fatalf("avatar upload returned %d, want %d: %s", response.StatusCode(), want, response.Body)
		}
	}
	upload([]byte("not an image"), http.StatusBadRequest)
	unchanged, err := engine.Avatar(ctx, account.Account.ID, "alice@localhost")
	if err != nil || unchanged != next {
		t.Fatalf("invalid upload changed the active photo: %+v %v", unchanged, err)
	}

	var uploadPNG bytes.Buffer
	replacement := image.NewNRGBA(image.Rect(0, 0, 256, 128))
	replacement.SetNRGBA(0, 0, color.NRGBA{G: 255, A: 255})
	if err := png.Encode(&uploadPNG, replacement); err != nil {
		t.Fatal(err)
	}
	upload(uploadPNG.Bytes(), http.StatusNoContent)
	updated, err := engine.Avatar(ctx, account.Account.ID, "alice@localhost")
	if err != nil || updated.ID != first.ID || updated.ImageHash == next.ImageHash {
		t.Fatalf("confirmed upload did not update the selected account: %+v %v", updated, err)
	}
	retained, err := engine.AvatarImage(ctx, updated.ImageHash)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(retained))
	if err != nil || config.Width != 64 || config.Height != 32 {
		t.Fatalf("unexpected thumbnail geometry: %+v %v", config, err)
	}

	// Independently read the active metadata and image from a second XMPP session.
	updatedSource := strings.TrimPrefix(updated.SourceToken, "pep:")
	readPublished := func(query string, target any) {
		t.Helper()
		wire, err := publisher.SendIQ(ctx, stanza.IQ{To: own, Type: stanza.GetIQ}.Wrap(xml.NewDecoder(strings.NewReader(query))))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = wire.Close() }()
		if err := xml.NewTokenDecoder(wire).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
	var active struct {
		stanza.IQ
		Items []struct {
			ID string `xml:"id,attr"`
		} `xml:"pubsub>items>item"`
	}
	readPublished(`<pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="urn:xmpp:avatar:metadata" max_items="1"/></pubsub>`, &active)
	if active.Type != stanza.ResultIQ || len(active.Items) != 1 || active.Items[0].ID != updatedSource {
		t.Fatalf("server did not activate the uploaded photo: %+v", active)
	}
	var wireData struct {
		stanza.IQ
		Data string `xml:"pubsub>items>item>data"`
	}
	readPublished(`<pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="urn:xmpp:avatar:data"><item id="`+updatedSource+`"/></items></pubsub>`, &wireData)
	if wireData.Type != stanza.ResultIQ || wireData.Data != base64.StdEncoding.EncodeToString(retained) {
		t.Fatalf("server did not retain the uploaded photo: %v", wireData.Type)
	}
	upload(uploadPNG.Bytes(), http.StatusNoContent)
	replayed, err := engine.Avatar(ctx, account.Account.ID, "alice@localhost")
	if err != nil || replayed != updated {
		t.Fatalf("repeating the same upload changed the retained revision: %+v %v", replayed, err)
	}
	if _, err = engine.Disconnect(ctx, account.Account.ID); err != nil {
		t.Fatal(err)
	}
	upload(uploadPNG.Bytes(), http.StatusConflict)
	connect()
	wait("pep:" + updatedSource)
	// A subsequent provider change proves the reconnected worker is receiving PEP events.
	t.Log("reconnected fixture, clearing avatar")
	metadata(updatedSource, "")
	cleared := wait("pep:none")
	if cleared.ImageHash != "" || cleared.ID != first.ID {
		t.Fatal("published removal did not clear avatar")
	}
	if _, err = engine.AvatarImage(ctx, next.ImageHash); err != nil {
		t.Fatal("cache lost retained image on clear", err)
	}
}
