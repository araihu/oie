package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/goshtoso/components/avatar"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func avatarPNG(t *testing.T, width int) ([]byte, string) {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, width, 1))); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(out.Bytes())
	return out.Bytes(), hex.EncodeToString(sum[:])
}

func TestAvatarProxyRestrictsContentAndCaching(t *testing.T) {
	body, hash := avatarPNG(t, 1)
	var calls int
	var condition string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		condition = r.Header.Get("If-None-Match")
		if r.URL.Path != "/api/v1/avatars/"+hash {
			t.Errorf("unexpected image API path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Set-Cookie", "provider=secret")
		if condition == `"`+hash+`"` {
			w.WriteHeader(304)
			return
		}
		_, _ = w.Write(body)
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	get := func(method, value string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/ui/avatars/"+hash, nil)
		if value != "" {
			r.Header.Set("If-None-Match", value)
		}
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	w := get("GET", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("wrong image response: %d %v", w.Code, w.Header())
	}
	if w.Header().Get("ETag") != `"`+hash+`"` || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("image cache policy or provider header isolation failed")
	}
	w = get("GET", `"`+hash+`"`)
	if w.Code != 304 || w.Body.Len() != 0 || condition != `"`+hash+`"` {
		t.Fatal("conditional request not forwarded")
	}
	if w = get("HEAD", ""); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD returned image body")
	}
	previous := calls
	for _, invalid := range []string{"short", strings.ToUpper(hash), hash + "/extra", strings.Repeat("g", 64)} {
		w = httptest.NewRecorder()
		ui.ServeHTTP(w, httptest.NewRequest("GET", "/ui/avatars/"+invalid, nil))
		if w.Code != 404 {
			t.Errorf("invalid hash status %d", w.Code)
		}
	}
	if calls != previous {
		t.Fatal("invalid hash reached engine")
	}
}

func TestAvatarProxyRejectsRedirectsAndUnverifiedImages(t *testing.T) {
	pngBody, hash := avatarPNG(t, 1)
	large, largeHash := avatarPNG(t, 257)
	externalCalls := 0
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalCalls++; _, _ = w.Write(pngBody) }))
	defer external.Close()
	for _, tc := range []struct {
		name, mime, location, hash string
		status                     int
		body                       []byte
		want                       int
	}{
		{name: "provider redirect", status: 302, location: external.URL, hash: hash, want: 503},
		{name: "SVG", status: 200, mime: "image/svg+xml", hash: hash, body: []byte(`<svg onload="alert(1)"/>`), want: 502},
		{name: "mismatched hash", status: 200, mime: "image/png", hash: strings.Repeat("a", 64), body: pngBody, want: 502},
		{name: "oversized dimensions", status: 200, mime: "image/png", hash: largeHash, body: large, want: 502},
		{name: "malformed PNG", status: 200, mime: "image/png", hash: hash, body: []byte("not a png"), want: 502},
		{name: "excess bytes", status: 200, mime: "image/png", hash: hash, body: make([]byte, (1<<20)+1), want: 502},
		{name: "missing image", status: 404, hash: hash, want: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.mime)
				w.Header().Set("Location", tc.location)
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			ui.ServeHTTP(w, httptest.NewRequest("GET", "/ui/avatars/"+tc.hash, nil))
			if w.Code != tc.want || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected failure policy: %d %v", w.Code, w.Header())
			}
		})
	}
	if externalCalls != 0 {
		t.Fatal("avatar proxy followed an external redirect")
	}
}

func TestAvatarMetadataPaginationIdentityAndFallback(t *testing.T) {
	_, hash := avatarPNG(t, 1)
	opaque := `room/'\"<script>@service`
	pages := 0
	next := testRevision
	failing := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		pages++
		if r.URL.Query().Get("after") == "" {
			_ = json.NewEncoder(w).Encode(engineapi.AvatarPage{Items: []engineapi.Avatar{{AccountId: testAccount, Address: opaque, ImageHash: hash}}, NextCursor: &next})
			return
		}
		if failing {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(engineapi.AvatarPage{Items: []engineapi.Avatar{{AccountId: testAccount, Address: "removed@service", ImageHash: ""}, {AccountId: testEngine, Address: opaque, ImageHash: strings.Repeat("b", 64)}}})
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	v := ui.baseView(httptest.NewRequest("GET", "/", nil))
	ui.loadAvatars(context.Background(), &v, testAccount)
	if pages != 2 || len(v.AvatarAccounts) != 1 || v.avatarSource(testAccount, opaque) != "/ui/avatars/"+hash || v.avatarSource(testEngine, opaque) != "" {
		t.Fatal("pagination or account-scoped opaque identity failed")
	}
	if value, ok := v.Avatars[avatarIdentity{testAccount, "removed@service"}]; !ok || value != "" {
		t.Fatal("explicit removal was discarded")
	}
	var rendered bytes.Buffer
	if err := profileAvatar(v, testAccount, opaque, avatar.Config{Name: "Room"}).Render(context.Background(), &rendered); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.String(), opaque) || !strings.Contains(rendered.String(), `x-if="oieAvatarSrc"`) {
		t.Fatal("opaque address entered executable expression or public reactive Avatar was lost")
	}
	v.Selected = &engineapi.Conversation{Kind: engineapi.ConversationKindRoom, Address: opaque, AccountId: testAccount}
	m := messageView{Message: engineapi.Message{AccountId: testAccount, Sender: engineapi.Sender{DisplayName: opaque}}}
	if v.messageAvatarAddress(m) != "" {
		t.Fatal("room nickname resolved to a participant image")
	}
	v.Selected.Kind = engineapi.ConversationKindDirect
	if v.messageAvatarAddress(m) != opaque {
		t.Fatal("direct message did not use conversation identity")
	}
	failing = true
	v = ui.baseView(httptest.NewRequest("GET", "/", nil))
	ui.loadAvatars(context.Background(), &v, testAccount)
	if len(v.AvatarAccounts) != 0 || len(v.Avatars) != 0 {
		t.Fatal("partial metadata fetch was published as a complete snapshot")
	}
}
