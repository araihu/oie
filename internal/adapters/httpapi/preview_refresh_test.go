package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type refreshFixture struct {
	mu                   sync.Mutex
	data                 []byte
	title                string
	fail                 bool
	calls, invalidations int
	entered, release     chan struct{}
}

func (f *refreshFixture) InvalidateLink(string) { f.mu.Lock(); f.invalidations++; f.mu.Unlock() }
func (f *refreshFixture) Image(context.Context, string) ([]byte, error) {
	return nil, application.PreviewFailure{Code: "unsupported"}
}
func (f *refreshFixture) Link(ctx context.Context, _ string) (application.PreviewMetadata, error) {
	f.mu.Lock()
	f.calls++
	title, fail, entered, release := f.title, f.fail, f.entered, f.release
	f.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return application.PreviewMetadata{}, ctx.Err()
		}
	}
	if fail {
		return application.PreviewMetadata{}, application.PreviewFailure{Code: "fetch_failed"}
	}
	return application.PreviewMetadata{Title: title, SiteName: "Example", Favicon: f.data}, nil
}

func TestExplicitLinkRefreshPreservesSourceAndCoalescesPendingWork(t *testing.T) {
	dir := t.TempDir()
	e, store, server := newMessagingEngine(t, dir, &protocolStub{})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewGray(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	fetch := &refreshFixture{data: buffer.Bytes(), title: "Original title"}
	e.SetPreviewServices(fetch, mediafiles.New(dir+"/media/images"))
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: "bob@localhost", ProviderID: "link-refresh", OriginalText: "Read https://example.com/article"}); err != nil {
		t.Fatal(err)
	}
	rows, err := e.ConversationPreviews(t.Context(), c.ID, nil)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	previewID := rows[0].ID
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	wait := func(state string) domain.MessagePreview {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			p, err := e.Preview(t.Context(), previewID)
			if err == nil && p.State == state && !p.RefreshRequested {
				return p
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("preview state timeout", state)
		return domain.MessagePreview{}
	}
	first := wait("ready")
	if first.FaviconHash == "" || first.ImageHash != "" {
		t.Fatal("icon must remain separate from preview image", first)
	}
	original, err := e.Message(t.Context(), first.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	fetch.mu.Lock()
	fetch.title = "Updated title"
	fetch.entered = make(chan struct{}, 1)
	fetch.release = make(chan struct{})
	entered, release := fetch.entered, fetch.release
	fetch.mu.Unlock()
	response, err := http.Post(server.URL+"/api/v1/previews/"+string(previewID)+"/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var accepted engineapi.MessagePreview
	if err = json.NewDecoder(response.Body).Decode(&accepted); err != nil || response.StatusCode != 202 {
		t.Fatal(response.StatusCode, err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh worker did not start")
	}
	for i := 0; i < 4; i++ {
		queued, err := e.RefreshPreview(t.Context(), previewID)
		if err != nil || string(queued.RevisionID) != accepted.RevisionId || queued.Title != first.Title || queued.FaviconHash != first.FaviconHash {
			t.Fatal("pending refresh was duplicated or metadata cleared", queued, err)
		}
	}
	close(release)
	updated := wait("ready")
	if updated.Title != "Updated title" || updated.ID != first.ID || updated.RevisionID == first.RevisionID {
		t.Fatal(updated)
	}
	current, err := e.Message(t.Context(), first.MessageID)
	if err != nil || current.OriginalText != original.OriginalText || current.RevisionID != original.RevisionID {
		t.Fatal("refresh mutated source message", err)
	}
	fetch.mu.Lock()
	fetch.entered = nil
	fetch.release = nil
	fetch.fail = true
	fetch.mu.Unlock()
	if _, err = e.RefreshPreview(t.Context(), previewID); err != nil {
		t.Fatal(err)
	}
	failed := wait("unavailable")
	if failed.Title != updated.Title || failed.FaviconHash != updated.FaviconHash {
		t.Fatal("failure discarded last-known metadata")
	}
	stale := failed
	stale.URL = "https://example.com/not-in-message"
	stale.RevisionID = id()
	if err = store.SavePreview(t.Context(), stale, failed.RevisionID); err != nil {
		t.Fatal(err)
	}
	fetch.mu.Lock()
	calls := fetch.calls
	fetch.mu.Unlock()
	if _, err = e.RefreshPreview(t.Context(), previewID); err == nil {
		t.Fatal("stale reference accepted")
	}
	fetch.mu.Lock()
	defer fetch.mu.Unlock()
	if fetch.calls != calls {
		t.Fatal("stale reference fetched provider")
	}
}
