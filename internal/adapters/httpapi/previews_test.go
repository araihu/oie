package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type previewFixture struct {
	links, images atomic.Int32
	data          []byte
}

func (f *previewFixture) Link(context.Context, string) (application.PreviewMetadata, error) {
	f.links.Add(1)
	return application.PreviewMetadata{Title: "Shared article", Description: "Plain metadata", SiteName: "Fixture", Image: f.data}, nil
}
func (f *previewFixture) Image(context.Context, string) ([]byte, error) {
	f.images.Add(1)
	return f.data, nil
}

func TestPreviewLifecycleDoesNotDownloadOriginalUntilExplicitDemand(t *testing.T) {
	dir := t.TempDir()
	e, store, server := newMessagingEngine(t, dir, &protocolStub{})
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewGray(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	fetch := &previewFixture{data: data.Bytes()}
	e.SetPreviewServices(fetch, mediafiles.New(dir+"/media/images"))
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: "bob@localhost", ProviderID: "preview-source", OriginalText: "https://example.com/article https://example.com/photo.png"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	get := func(path string) []byte {
		t.Helper()
		res, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatal("GET", res.StatusCode, string(body))
		}
		return body
	}
	var page engineapi.PreviewPage
	if err := json.Unmarshal(get("/api/v1/conversations/"+string(c.ID)+"/previews"), &page); err != nil || len(page.Items) != 2 {
		t.Fatal("batch preview contract", page, err)
	}
	var link, img engineapi.MessagePreview
	for _, p := range page.Items {
		if p.Kind == "image" {
			img = p
		} else {
			link = p
		}
	}
	wait := func(id string, ready func(engineapi.MessagePreview) bool) engineapi.MessagePreview {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var p engineapi.MessagePreview
			if err := json.Unmarshal(get("/api/v1/previews/"+id), &p); err != nil {
				t.Fatal(err)
			}
			if ready(p) {
				return p
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("preview worker timed out")
		return engineapi.MessagePreview{}
	}
	link = wait(link.Id, func(p engineapi.MessagePreview) bool { return p.State == "ready" })
	if link.Title == nil || *link.Title != "Shared article" || link.ImageHash == nil || fetch.images.Load() != 0 {
		t.Fatal("automatic original fetch or missing card")
	}
	for i := 0; i < 5; i++ {
		res, err := http.Post(server.URL+"/api/v1/previews/"+img.Id+"/download", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 202 && res.StatusCode != 200 {
			t.Fatal("download ensure", res.StatusCode)
		}
	}
	loaded := wait(img.Id, func(p engineapi.MessagePreview) bool { return p.DownloadState == "ready" })
	if fetch.images.Load() != 1 || loaded.FileHash == nil || loaded.ImageHash == nil || loaded.Integrity == nil || *loaded.Integrity != "computed_only" {
		t.Fatal("download deduplication/integrity", loaded)
	}
	if loaded.Id != img.Id || loaded.RevisionId == img.RevisionId {
		t.Fatal("preview entity/revision identity changed incorrectly")
	}
	// A renderer upgrade must reuse retained original bytes, without another GET
	// to the provider or any change to the source message.
	legacy, err := e.Preview(t.Context(), domain.ID(img.Id))
	if err != nil {
		t.Fatal(err)
	}
	expected := legacy.RevisionID
	legacy.RevisionID = id()
	legacy.PreviewVersion = 0
	if err = store.SavePreview(t.Context(), legacy, expected); err != nil {
		t.Fatal(err)
	}
	get("/api/v1/conversations/" + string(c.ID) + "/previews")
	wait(img.Id, func(p engineapi.MessagePreview) bool {
		return p.State == "ready" && p.RevisionId != string(legacy.RevisionID)
	})
	if fetch.images.Load() != 1 {
		t.Fatal("thumbnail upgrade downloaded the original again")
	}

	if got := get("/api/v1/media/" + *loaded.FileHash); !bytes.Equal(got, data.Bytes()) {
		t.Fatal("retained original differs")
	}
	original, err := e.Message(t.Context(), domain.ID(img.MessageId))
	if err != nil || original.OriginalText != "https://example.com/article https://example.com/photo.png" {
		t.Fatal("preview changed message", err)
	}
}
