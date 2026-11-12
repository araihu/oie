package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/previewfetch"
)

func TestDraftLinkPreviewDoesNotCreateRetainedState(t *testing.T) {
	engine, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewGray(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	fetcher := &previewFixture{data: pngData.Bytes()}
	engine.SetPreviewServices(fetcher, nil)
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_, sequence, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	address := "https://example.com/studio#directions"
	result, err := client.ResolveDraftLinkPreviewWithResponse(ctx, engineapi.DraftLinkPreviewRequest{Url: address})
	if err != nil {
		t.Fatal(err)
	}
	preview := result.JSON200
	if preview == nil || preview.Url != address || preview.Title == nil || *preview.Title != "Shared article" ||
		preview.Description == nil || *preview.Description != "Plain metadata" || preview.Image == nil ||
		preview.Image.MediaType != "image/png" || preview.Image.Data != base64.StdEncoding.EncodeToString(pngData.Bytes()) {
		t.Fatalf("unexpected preview response: status %d", result.StatusCode())
	}
	for _, input := range []struct {
		url    string
		status int
	}{
		{"javascript:alert(1)", 400},
		{"https://user:secret@example.com/", 400},
		{"https://example.com/archive.zip", 204},
	} {
		result, err := client.ResolveDraftLinkPreviewWithResponse(ctx, engineapi.DraftLinkPreviewRequest{Url: input.url})
		if err != nil || result.StatusCode() != input.status {
			t.Fatalf("invalid or unsupported draft URL was not rejected: %v, %v", result, err)
		}
	}
	if fetcher.links.Load() != 1 || fetcher.images.Load() != 0 {
		t.Fatalf("unexpected lookups: %d links and %d originals", fetcher.links.Load(), fetcher.images.Load())
	}
	_, after, err := store.Snapshot(ctx)
	if err != nil || after != sequence {
		t.Fatalf("draft preview changed retained state: %d -> %d, %v", sequence, after, err)
	}
}

func TestDraftLinkPreviewRejectsPrivateURL(t *testing.T) {
	engine, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	engine.SetPreviewServices(previewfetch.New(), nil)
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ResolveDraftLinkPreviewWithResponse(t.Context(), engineapi.DraftLinkPreviewRequest{Url: "http://127.0.0.1/"})
	if err != nil || result.StatusCode() != 204 {
		t.Fatalf("private address must fall back: %v, %v", result, err)
	}
}
