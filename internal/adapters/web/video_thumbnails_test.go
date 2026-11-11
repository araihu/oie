package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestVideoThumbnailBFFMaintainsCSRFEngineAndSourceBinding(t *testing.T) {
	reads, writes := 0, 0
	sourceID, foreignID := testRevision, testAccount
	preview := engineapi.VideoThumbnail{Id: testMessage, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat, MessageId: testMessage, SharedContentId: sourceID, Kind: "video", State: "not_requested", Url: "https://example.test/movie.mp4", UpdatedAt: time.Now().UTC()}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/engine" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine})
			return
		}
		if r.URL.Path != "/api/v1/shared-content/"+testRevision+"/thumbnail" {
			t.Errorf("unexpected API path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Method == "POST" {
			writes++
			preview.State = "queued"
			w.WriteHeader(202)
		} else {
			reads++
		}
		_ = json.NewEncoder(w).Encode(preview)
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	get := func(engine string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/ui/api/v1/shared-content/"+testRevision+"/thumbnail", nil)
		r.Header.Set("X-Oie-Engine-ID", engine)
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	if get("").Code != 400 || get(testAccount).Code != 409 || reads != 0 {
		t.Fatal("foreign engine reached thumbnail state")
	}
	if w := get(testEngine); w.Code != 200 || reads != 1 || writes != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cache read effects", w.Code, reads, writes)
	}
	seed := httptest.NewRecorder()
	token := csrfToken(seed, httptest.NewRequest("GET", "/", nil))
	cookie := seed.Result().Cookies()[0]
	post := func(csrf, engine string) *httptest.ResponseRecorder {
		body := url.Values{"csrf": {csrf}, "engineId": {engine}, "itemId": {testRevision}}
		r := httptest.NewRequest("POST", "/ui/videos/thumbnail", strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", "application/json")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	if post("forged", testEngine).Code != 403 || post(token, testAccount).Code != 409 || writes != 0 {
		t.Fatal("rejected request reached thumbnail extraction")
	}
	if w := post(token, testEngine); w.Code != 202 || writes != 1 {
		t.Fatal("explicit ensure lost", w.Code, w.Body.String())
	}
	preview.SharedContentId = foreignID
	if get(testEngine).Code != 503 {
		t.Fatal("foreign reference status accepted")
	}
}
