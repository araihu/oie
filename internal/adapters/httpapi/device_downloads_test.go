package httpapi_test

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestCompleteDownloadVerifiesSenderAndRetainsPerDevice(t *testing.T) {
	dir := t.TempDir()
	e, store, server := newMessagingEngine(t, dir, &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	ctx := t.Context()
	body := []byte("file served in complete and partial responses")
	short, long := sha256.Sum256(body), sha512.Sum512(body)
	hash := hex.EncodeToString(short[:])
	filesDir := filepath.Join(dir, "media", "downloads")
	e.SetDownloadFiles(mediafiles.NewDownloads(filesDir))
	source := &streamedFixture{data: body, closed: make(chan struct{}, 32)}
	e.SetMediaReader(source)
	good, bad, plain := "https://files.example/good.mp4", "https://files.example/bad.mp4", "https://files.example/plain.mp4"
	for _, in := range []struct {
		url     string
		digests []domain.FileDigest
	}{
		{good, []domain.FileDigest{{URL: good, Algorithm: "sha-512", Value: hex.EncodeToString(long[:])}}},
		{bad, []domain.FileDigest{{URL: bad, Algorithm: "sha-256", Value: strings.Repeat("1", 64)}}},
		{plain, []domain.FileDigest{{URL: bad, Algorithm: "sha-256", Value: strings.Repeat("0", 64)}}},
	} {
		if err := e.Receive(ctx, application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: in.url, ProviderID: in.url, FileDigests: in.digests}); err != nil {
			t.Fatal(err)
		}
	}
	audio := "https://files.example/voice"
	if err := e.Receive(ctx, application.Incoming{
		AccountID: c.AccountID, Address: c.Address, OriginalText: "A voice note with a caption", ProviderID: "voice", AudioURLs: []string{audio},
		FileDigests: []domain.FileDigest{{URL: audio, Algorithm: "sha-256", Value: hash}},
	}); err != nil {
		t.Fatal(err)
	}
	clip := "https://files.example/clip"
	if err := e.Receive(ctx, application.Incoming{
		AccountID: c.AccountID, Address: c.Address, OriginalText: "A video with a caption", ProviderID: "clip",
		FileReferences: []domain.SharedReference{{URL: clip, Kind: "video", FileName: "trail.mp4"}},
		FileDigests:    []domain.FileDigest{{URL: clip, Algorithm: "sha-256", Value: hash}},
	}); err != nil {
		t.Fatal(err)
	}
	startMediaEngine(t, e)
	catalog := waitVideoCatalog(t, e)
	items := map[string]string{}
	for _, item := range catalog.Items {
		items[item.URL] = string(item.ID)
		if item.URL == clip && (item.Message.OriginalText != "A video with a caption" || item.FileName != "trail.mp4") {
			t.Fatalf("captioned attachment: %+v", item)
		}
	}
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	read := func(address, device, span string, download bool, status int) (http.Header, []byte) {
		t.Helper()
		p := &engineapi.GetSharedContentMediaParams{}
		if download {
			p.Download = &download
		}
		if device != "" {
			p.XOieDownloadDevice = &device
		}
		if span != "" {
			p.Range = &span
		}
		response, err := client.GetSharedContentMedia(ctx, items[address], p)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("%s: status %d, body %q, err %v", address, response.StatusCode, data, err)
		}
		return response.Header, data
	}
	audioPage, err := e.SharedContent(ctx, domain.SharedContentQuery{Type: "audio", Limit: 10})
	if err != nil || len(audioPage.Items) != 1 || audioPage.Items[0].URL != audio {
		t.Fatal("advertised audio without an extension or body URL", audioPage, err)
	}
	items[audio] = string(audioPage.Items[0].ID)
	audioHeaders, audioData := read(audio, "", "", true, http.StatusOK)
	if audioHeaders.Get("Content-Type") != "audio/mpeg" || audioHeaders.Get("X-Oie-Integrity") != "sender_verified" || !bytes.Equal(audioData, body) {
		t.Fatal("advertised audio did not deliver its complete verified bytes", audioHeaders)
	}
	clipHeaders, clipData := read(clip, "", "", true, http.StatusOK)
	if clipHeaders.Get("X-Oie-Integrity") != "sender_verified" || !bytes.Equal(clipData, body) {
		t.Fatal("captioned video did not deliver its complete verified bytes", clipHeaders)
	}
	deviceA, deviceB := string(id()), string(id())
	header, data := read(good, "", "bytes=0-3", false, http.StatusPartialContent)
	if header.Get("X-Oie-Integrity") != "" || !bytes.Equal(data, body[:4]) {
		t.Fatal("partial response reported original integrity", header, string(data))
	}
	header, data = read(bad, deviceA, "", true, http.StatusBadGateway)
	if header.Get("X-Oie-Integrity") != "mismatch" || bytes.Contains(data, body) {
		t.Fatal("mismatch returned original bytes or missing problem code", string(data))
	}
	checkDownload := func(url, device, integrity string) {
		t.Helper()
		header, data := read(url, device, "bytes=0-3", true, http.StatusOK)
		if header.Get("X-Oie-SHA256") != hash || header.Get("X-Oie-Integrity") != integrity || !bytes.Equal(data, body) {
			t.Fatalf("download reported %v or incorrect bytes", header)
		}
	}
	checkDownload(good, deviceA, "sender_verified")
	checkDownload(plain, deviceB, "computed_only")
	before := source.calls.Load()
	checkDownload(good, deviceA, "sender_verified")
	if source.calls.Load() != before {
		t.Fatal("a retained download was fetched again")
	}
	if err := os.WriteFile(filepath.Join(filesDir, hash), bytes.Repeat([]byte{'x'}, len(body)), 0600); err != nil {
		t.Fatal(err)
	}
	checkDownload(good, deviceA, "sender_verified")
	if source.calls.Load() != before+1 {
		t.Fatal("a corrupted retained download was not fetched again")
	}
	checkDownload(plain, deviceB, "computed_only")
	if source.calls.Load() != before+1 {
		t.Fatal("repair did not replace the shared corrupt file")
	}
	checkPolicy := func(device string, days, count int) {
		t.Helper()
		got, err := client.GetDownloadRetentionWithResponse(ctx, device)
		if err != nil || got.JSON200 == nil || got.JSON200.Days != days || got.JSON200.Count != count {
			t.Fatalf("policy %v, %v", got, err)
		}
	}
	checkPolicy(deviceA, 0, 1)
	checkPolicy(deviceB, 0, 1)
	policy, err := client.SetDownloadRetentionWithResponse(ctx, deviceA, engineapi.SetDownloadRetention{Days: 7})
	if err != nil || policy.JSON200 == nil {
		t.Fatal(policy, err)
	}
	clear := func(device string) {
		t.Helper()
		response, err := client.ClearDeviceDownloadsWithResponse(ctx, device)
		if err != nil || response.JSON200 == nil || response.JSON200.Count != 0 {
			t.Fatal(response, err)
		}
	}
	clear(deviceA)
	checkPolicy(deviceA, 7, 0)
	checkPolicy(deviceB, 0, 1)
	if _, err := os.Stat(filepath.Join(filesDir, hash)); err != nil {
		t.Fatal("removed another device's file", err)
	}
	clear(deviceB)
	if _, err := os.Stat(filepath.Join(filesDir, hash)); !os.IsNotExist(err) {
		t.Fatal("unreferenced file survived deletion", err)
	}
	oldPayload := []byte("a retained download older than a week")
	oldSum := sha256.Sum256(oldPayload)
	oldHash := hex.EncodeToString(oldSum[:])
	if err := os.WriteFile(filepath.Join(filesDir, oldHash), oldPayload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDownload(ctx, domain.Download{
		DeviceID: domain.ID(deviceA), SourceID: id(), SourceRevision: id(), Kind: "file", Hash: oldHash, MimeType: "video/mp4",
		Size: int64(len(oldPayload)), DownloadedAt: time.Now().UTC().AddDate(0, 0, -8),
	}); err != nil {
		t.Fatal(err)
	}
	checkPolicy(deviceA, 7, 1)
	policy, err = client.SetDownloadRetentionWithResponse(ctx, deviceA, engineapi.SetDownloadRetention{Days: 7})
	if err != nil || policy.JSON200 == nil || policy.JSON200.Count != 0 {
		t.Fatal("existing originals did not expire under the selected policy", policy, err)
	}
	if _, err := os.Stat(filepath.Join(filesDir, oldHash)); !os.IsNotExist(err) {
		t.Fatal("expired original survived retention cleanup", err)
	}
}

func TestDownloadedImageVerifiesOriginalBeforeRetention(t *testing.T) {
	dir := t.TempDir()
	e, _, server := newMessagingEngine(t, dir, &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	data := thumbnailPNG(t)
	sha := sha256.Sum256(data)
	e.SetPreviewServices(&previewFixture{data: data}, mediafiles.New(filepath.Join(dir, "media", "images")))
	good, bad := "https://example.com/good.png", "https://example.com/bad.png"
	if err := e.Receive(t.Context(), application.Incoming{
		AccountID: c.AccountID, Address: c.Address, OriginalText: good + " " + bad, ProviderID: "digests",
		FileDigests: []domain.FileDigest{
			{URL: good, Algorithm: "sha-256", Value: hex.EncodeToString(sha[:])},
			{URL: bad, Algorithm: "sha-256", Value: strings.Repeat("0", 64)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	ctx := t.Context()
	previews, err := client.ListConversationPreviewsWithResponse(ctx, string(c.ID), nil)
	if err != nil || previews.JSON200 == nil || len(previews.JSON200.Items) != 2 {
		t.Fatal(previews, err)
	}
	device := string(id())
	for _, p := range previews.JSON200.Items {
		response, err := client.DownloadPreviewImageWithResponse(ctx, p.Id, &engineapi.DownloadPreviewImageParams{XOieDownloadDevice: &device})
		if err != nil || response.JSON202 == nil {
			t.Fatal(response, err)
		}
	}
	startMediaEngine(t, e)
	for _, p := range previews.JSON200.Items {
		deadline := time.Now().Add(5 * time.Second)
		var result *engineapi.MessagePreview
		for time.Now().Before(deadline) {
			response, err := client.GetMessagePreviewWithResponse(ctx, p.Id)
			if err != nil || response.JSON200 == nil {
				t.Fatal(response, err)
			}
			result = response.JSON200
			if result.DownloadState == "ready" || result.DownloadState == "failed" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if p.Url == good {
			if result == nil || result.DownloadState != "ready" || result.VerifiedAlgorithm == nil || *result.VerifiedAlgorithm != "sha-256" || result.Integrity != nil {
				t.Fatal("verified original", result)
			}
		} else if result == nil || result.DownloadState != "failed" || result.FileHash != nil || result.LastErrorCode == nil || *result.LastErrorCode != "integrity_mismatch" {
			t.Fatal("corrupted original", result)
		}
	}
	policy, err := client.GetDownloadRetentionWithResponse(ctx, device)
	if err != nil || policy.JSON200 == nil || policy.JSON200.Count != 1 {
		t.Fatal(policy, err)
	}
	cleared, err := client.ClearDeviceDownloadsWithResponse(ctx, device)
	if err != nil || cleared.JSON200 == nil || cleared.JSON200.Count != 0 {
		t.Fatal(cleared, err)
	}
	for _, p := range previews.JSON200.Items {
		response, err := client.GetMessagePreviewWithResponse(ctx, p.Id)
		if err != nil || response.JSON200 == nil || response.JSON200.FileHash != nil || p.Url == good && response.JSON200.ImageHash == nil {
			t.Fatal("expected preview to survive original removal", response, err)
		}
	}
}
