package httpapi_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func thumbnailPNG(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 32, 24))); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestVideoThumbnailAPICachesCoalescesAndKeepsSourceAuthority(t *testing.T) {
	directory := t.TempDir()
	e, store, server := newMessagingEngine(t, directory, &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "thumb-source", OriginalText: "https://files.example/clip.mp4", MediaThumbnails: []domain.MediaThumbnail{{URL: "https://files.example/clip.mp4", ThumbnailURL: "https://files.example/poster.png"}}, Correction: &domain.MessageCorrection{Scope: "sender:peer", Author: "peer", ID: "thumb-source"}}); err != nil {
		t.Fatal(err)
	}
	files := mediafiles.New(directory + "/media/images")
	thumbnail := &suppliedThumbnailFixture{previewFixture: previewFixture{data: thumbnailPNG(t)}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	e.SetPreviewServices(thumbnail, files)
	reader := &streamedFixture{data: bytes.Repeat([]byte("video"), 100), closed: make(chan struct{}, 32)}
	e.SetMediaReader(reader)
	startMediaEngine(t, e)
	page := waitVideoCatalog(t, e)
	if len(page.Items) != 1 {
		t.Fatal(page)
	}
	item := page.Items[0]
	client, _ := engineapi.NewClientWithResponses(server.URL)
	initial, err := client.GetVideoThumbnailWithResponse(t.Context(), string(item.ID))
	if err != nil || initial.JSON200 == nil || initial.JSON200.Kind != "video" || initial.JSON200.State != "not_requested" || initial.JSON200.SharedContentId != string(item.ID) {
		t.Fatal(initial, err)
	}
	requested, err := client.RequestVideoThumbnailWithResponse(t.Context(), string(item.ID))
	if err != nil || requested.StatusCode() != 202 {
		t.Fatal(requested, err)
	}
	select {
	case <-thumbnail.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("thumbnail job did not start")
	}
	var requests sync.WaitGroup
	for range 8 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			result, callErr := client.RequestVideoThumbnailWithResponse(t.Context(), string(item.ID))
			if callErr != nil || result.StatusCode() != 202 {
				t.Error("coalesced request", callErr)
			}
		}()
	}
	requests.Wait()
	if thumbnail.calls.Load() != 1 || reader.calls.Load() != 0 {
		t.Fatal("duplicate thumbnail work or a video read", thumbnail.calls.Load(), reader.calls.Load())
	}
	close(thumbnail.release)
	var ready *engineapi.VideoThumbnail
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, readErr := client.GetVideoThumbnailWithResponse(t.Context(), string(item.ID))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if result.JSON200 != nil && result.JSON200.State == "ready" {
			ready = result.JSON200
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ready == nil || ready.ImageHash == nil {
		t.Fatal("thumbnail retained as original", ready)
	}
	content, err := client.GetMediaImageWithResponse(t.Context(), *ready.ImageHash, nil)
	if err != nil || content.StatusCode() != 200 {
		t.Fatal(content, err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(content.Body))
	if err != nil || format != "png" || config.Width != 32 {
		t.Fatal(config, format, err)
	}
	again, err := client.RequestVideoThumbnailWithResponse(t.Context(), string(item.ID))
	if err != nil || again.StatusCode() != 200 || thumbnail.calls.Load() != 1 {
		t.Fatal("cache miss after retention", again, err)
	}
	wrong, err := client.RequestVideoThumbnailWithResponse(t.Context(), string(id()))
	if err != nil || wrong.StatusCode() != 404 {
		t.Fatal("unknown source accepted", wrong, err)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 || thumbnail.links.Load() != 0 || thumbnail.images.Load() != 0 {
		t.Fatal("thumbnail crossed message/image pipeline", pending, err)
	}
	if err = e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "thumb-edit", OriginalText: "video removed", Correction: &domain.MessageCorrection{Scope: "sender:peer", Author: "peer", ID: "thumb-edit", TargetID: "thumb-source"}}); err != nil {
		t.Fatal(err)
	}
	removed, err := client.GetVideoThumbnailWithResponse(t.Context(), string(item.ID))
	if err != nil || removed.StatusCode() != 404 {
		t.Fatal("removed source retained authority", removed, err)
	}
	if reader.calls.Load() != 0 {
		t.Fatal("removed source caused network request")
	}
}

type suppliedThumbnailFixture struct {
	previewFixture
	calls   atomic.Int32
	address string
	entered chan struct{}
	release chan struct{}
}

func (f *suppliedThumbnailFixture) SmallThumbnail(ctx context.Context, address string) ([]byte, error) {
	f.calls.Add(1)
	f.address = address
	if f.entered != nil {
		f.entered <- struct{}{}
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.data, nil
}

func TestVideoThumbnailPrefersRetainedProtocolImageWithoutDecoder(t *testing.T) {
	dir := t.TempDir()
	e, _, server := newMessagingEngine(t, dir, &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	source := "https://files.example/protocol.mp4"
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "protocol-poster", OriginalText: source, MediaThumbnails: []domain.MediaThumbnail{{URL: "https://unrelated.example/clip.mp4", ThumbnailURL: "https://files.example/unrelated.png"}, {URL: source, ThumbnailURL: "file:///etc/passwd"}, {URL: source, ThumbnailURL: "https://files.example/supplied.png"}}}); err != nil {
		t.Fatal(err)
	}
	fetcher := &suppliedThumbnailFixture{previewFixture: previewFixture{data: thumbnailPNG(t)}}
	e.SetPreviewServices(fetcher, mediafiles.New(dir+"/media/images"))
	startMediaEngine(t, e)
	page := waitVideoCatalog(t, e)
	item := page.Items[0]
	// The accepted metadata survived SQLite storage and is tied to the exact body URL.
	retained, err := e.Message(t.Context(), item.Message.ID)
	if err != nil || len(retained.MediaThumbnails) != 1 {
		t.Fatal(retained.MediaThumbnails, err)
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	queued, err := client.RequestVideoThumbnailWithResponse(t.Context(), string(item.ID))
	if err != nil || queued.StatusCode() != 202 {
		t.Fatal(queued, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, err := client.GetVideoThumbnailWithResponse(t.Context(), string(item.ID))
		if err != nil {
			t.Fatal(err)
		}
		if result.JSON200 != nil && result.JSON200.State == "ready" {
			if fetcher.calls.Load() != 1 || fetcher.address != "https://files.example/supplied.png" || fetcher.images.Load() != 0 {
				t.Fatal("incorrect thumbnail source")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("supplied thumbnail did not become ready")
}

func TestVideoThumbnailRejectsSourceRemovedDuringRetrieval(t *testing.T) {
	dir := t.TempDir()
	e, store, _ := newMessagingEngine(t, dir, &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "pending-video", OriginalText: "https://files.example/pending.mp4", MediaThumbnails: []domain.MediaThumbnail{{URL: "https://files.example/pending.mp4", ThumbnailURL: "https://files.example/poster.png"}}, Correction: &domain.MessageCorrection{Scope: "sender:peer", Author: "peer", ID: "pending-video"}}); err != nil {
		t.Fatal(err)
	}
	thumbnail := &suppliedThumbnailFixture{previewFixture: previewFixture{data: thumbnailPNG(t)}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	e.SetPreviewServices(thumbnail, mediafiles.New(dir+"/media/images"))
	startMediaEngine(t, e)
	item := waitVideoCatalog(t, e).Items[0]
	queued, err := e.RequestVideoThumbnail(t.Context(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-thumbnail.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("thumbnail retrieval not started")
	}
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "removed-video", OriginalText: "removed", Correction: &domain.MessageCorrection{Scope: "sender:peer", Author: "peer", ID: "removed-video", TargetID: "pending-video"}}); err != nil {
		t.Fatal(err)
	}
	close(thumbnail.release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, err := store.Preview(t.Context(), queued.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.State == "unavailable" {
			if result.ImageHash != "" || result.LastErrorCode != "not_found" {
				t.Fatal(result)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stale frame remained queued")
}

func TestVideoThumbnailQueuedWorkAndCacheSurviveEngineRestart(t *testing.T) {
	dir := t.TempDir()
	e, store, initialServer := newMessagingEngine(t, dir, &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "restart-video", OriginalText: "https://files.example/restart.mp4", MediaThumbnails: []domain.MediaThumbnail{{URL: "https://files.example/restart.mp4", ThumbnailURL: "https://files.example/poster.png"}}}); err != nil {
		t.Fatal(err)
	}
	files := mediafiles.New(dir + "/media/images")
	thumbnail := &suppliedThumbnailFixture{previewFixture: previewFixture{data: thumbnailPNG(t)}}
	e.SetPreviewServices(thumbnail, files)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	item := waitVideoCatalog(t, e).Items[0]
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	queued, err := e.RequestVideoThumbnail(t.Context(), item.ID)
	if err != nil || queued.State != "queued" {
		t.Fatal(queued, err)
	}
	if thumbnail.calls.Load() != 0 {
		t.Fatal("stopped engine executed queued work")
	}
	initialServer.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, resumedStore, resumedServer := newMessagingEngine(t, dir, &protocolStub{})
	resumed.SetPreviewServices(thumbnail, files)
	ctx2, cancel2 := context.WithCancel(t.Context())
	done2 := make(chan error, 1)
	go func() { done2 <- resumed.Run(ctx2) }()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel2()
			if err := <-done2; err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ready, err := resumed.VideoThumbnail(t.Context(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ready.State == "ready" {
			stop()
			resumedServer.Close()
			if err := resumedStore.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, _, _ := newMessagingEngine(t, dir, &protocolStub{})
			reopened.SetPreviewServices(&previewFixture{}, files)
			cached, err := reopened.RequestVideoThumbnail(t.Context(), item.ID)
			if err != nil || cached.State != "ready" || cached.ImageHash != ready.ImageHash || thumbnail.calls.Load() != 1 {
				t.Fatal("retained cache fetched another thumbnail", cached, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued thumbnail did not resume")
}
