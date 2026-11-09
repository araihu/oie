package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestPreviewStatusReadsCanonicalMetadataWithoutDownload(t *testing.T) {
	reads, writes := 0, 0
	preview := engineapi.MessagePreview{Id: testRevision, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat, MessageId: testMessage, Url: "https://example.test/photo.png", Kind: "image", State: "ready", DownloadState: "queued", UpdatedAt: time.Now().UTC()}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			writes++
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/api/v1/engine" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine})
			return
		}
		if r.URL.Path != "/api/v1/previews/"+testRevision {
			t.Errorf("unexpected read %s", r.URL)
			w.WriteHeader(404)
			return
		}
		reads++
		_ = json.NewEncoder(w).Encode(preview)
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, engine string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if engine != "" {
			r.Header.Set("X-Oie-Engine-ID", engine)
		}
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/previews/" + testRevision
	for _, engine := range []string{"", testEngine} {
		w := get(path, engine)
		var got engineapi.MessagePreview
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Id != testRevision || got.DownloadState != "queued" {
			t.Fatalf("canonical state lost: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("metadata cache boundary missing")
		}
	}
	before := reads
	for _, tc := range []struct {
		path, engine string
		want         int
	}{{"/api/v1/previews/not-a-uuid", "", 400}, {path, "not-a-uuid", 400}, {path, testAccount, 409}} {
		if w := get(tc.path, tc.engine); w.Code != tc.want {
			t.Fatalf("guard status %d want %d", w.Code, tc.want)
		}
	}
	if reads != before {
		t.Fatal("invalid identity reached preview read")
	}
	preview.Id = testMessage
	if w := get(path, testEngine); w.Code != 503 {
		t.Fatal("foreign preview accepted", w.Code)
	}
	if writes != 0 {
		t.Fatal("metadata read mutated engine")
	}
}
