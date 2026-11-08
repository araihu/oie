package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/mediafiles"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type streamedFixture struct {
	calls  atomic.Int32
	closed chan struct{}
	data   []byte
	mu     sync.Mutex
	last   application.MediaReadRequest
}
type streamedBody struct {
	io.Reader
	once   sync.Once
	closed chan struct{}
}

func (b *streamedBody) Close() error { b.once.Do(func() { b.closed <- struct{}{} }); return nil }
func (f *streamedFixture) Open(_ context.Context, _ string, r application.MediaReadRequest) (application.MediaRead, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.last = r
	f.mu.Unlock()
	total := int64(len(f.data))
	start, end := int64(0), total-1
	partial := r.Range != nil && (r.Condition.ETag == "" || r.Condition.ETag == `"clip-v1"`)
	if partial {
		var ok bool
		start, end, ok = r.Range.Resolve(total)
		if !ok {
			return application.MediaRead{}, application.MediaFailure{Code: "range_not_satisfiable", Total: total}
		}
	}
	body := &streamedBody{Reader: bytes.NewReader(f.data[start : end+1]), closed: f.closed}
	mimeType := "video/mp4"
	if r.Format == "pdf" {
		mimeType = "application/pdf"
	} else if r.Format != "" && r.Format != "video" {
		mimeType = "text/plain"
	}
	return application.MediaRead{Body: body, MediaType: mimeType, Length: end - start + 1, Total: total, Offset: start, Partial: partial, RangeSupported: true, ETag: `"clip-v1"`, ModifiedAt: time.Unix(1000, 0)}, nil
}
func startMediaEngine(t *testing.T, e *application.Engine) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
}
func waitVideoCatalog(t *testing.T, e *application.Engine) domain.SharedContentPage {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		page, err := e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "video", Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if page.Indexing.State == "complete" {
			return page
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("video catalog did not index")
	return domain.SharedContentPage{}
}
func TestSharedMediaHTTPRangesScopeAndNoSpeculation(t *testing.T) {
	dir := t.TempDir()
	e, store, server := newMessagingEngine(t, dir, &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	preview := &previewFixture{}
	e.SetPreviewServices(preview, mediafiles.New(dir+"/media/images"))
	stream := &streamedFixture{data: []byte("0123456789abcdefghijklmnopqrstuvwxyz"), closed: make(chan struct{}, 32)}
	e.SetMediaReader(stream)
	for i, body := range []string{"https://files.example/older.mp4", "https://files.example/newer.webm", "https://files.example/report.docx", "https://files.example/voice.ogg"} {
		incoming := application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: fmt.Sprint("source-", i), OriginalText: body, Historical: true, CreatedAt: time.Unix(int64(1000+i), 0)}
		if i == 0 {
			incoming.Correction = &domain.MessageCorrection{Scope: "sender:peer", Author: "peer", ID: "source-0"}
		}
		if err := e.Receive(t.Context(), incoming); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := e.Messages(t.Context(), c.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var oldest domain.Message
	for _, m := range messages {
		if m.OriginalText == "https://files.example/older.mp4" {
			oldest = m
		}
	}
	// Retire an old queued video link card without invoking the generic link fetcher.
	legacy, err := store.EnsurePreview(t.Context(), domain.MessagePreview{ID: id(), RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, MessageID: oldest.ID, URL: oldest.OriginalText, Kind: "link", State: "queued", DownloadState: "not_downloaded", UpdatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	startMediaEngine(t, e)
	page := waitVideoCatalog(t, e)
	if len(page.Items) != 2 || stream.calls.Load() != 0 {
		t.Fatal("listing downloaded video", len(page.Items), stream.calls.Load())
	}
	client, _ := engineapi.NewClientWithResponses(server.URL)
	previews, err := client.ListConversationPreviewsWithResponse(t.Context(), string(c.ID), nil)
	if err != nil || previews.JSON200 == nil || len(previews.JSON200.Items) != 0 {
		t.Fatal("file references were registered as link previews", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		p, err := e.Preview(t.Context(), legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.State == "unavailable" && p.LastErrorCode == "unsupported" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	retired, err := e.Preview(t.Context(), legacy.ID)
	if err != nil || retired.State != "unavailable" || preview.links.Load() != 0 || preview.images.Load() != 0 {
		t.Fatal("legacy job contacted provider", retired, err)
	}
	first := page.Items[0]
	one := 1
	kind := engineapi.ListSharedContentParamsType("video")
	selected := []engineapi.ID{string(oldest.ID)}
	conversation := string(c.ID)
	scoped, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{Type: &kind, ConversationId: &conversation, MessageId: &selected, Limit: &one})
	if err != nil || scoped.JSON200 == nil || len(scoped.JSON200.Items) != 1 || scoped.JSON200.Items[0].Message.Id != string(oldest.ID) || scoped.JSON200.Indexing.TotalMessages != 1 {
		t.Fatal("message filter applied after pagination", scoped, err)
	}
	missingScope, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{MessageId: &selected})
	if err != nil || missingScope.StatusCode() != 400 {
		t.Fatal("message IDs accepted without conversation", err)
	}
	duplicate := []engineapi.ID{string(oldest.ID), string(oldest.ID)}
	invalid, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{ConversationId: &conversation, MessageId: &duplicate})
	if err != nil || invalid.StatusCode() != 400 {
		t.Fatal("duplicate source IDs accepted", err)
	}

	request := func(method, item, rangeValue, condition string) (*http.Response, []byte, error) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+"/api/v1/shared-content/"+item+"/content", nil)
		if err != nil {
			t.Fatal(err)
		}
		if rangeValue != "" {
			req.Header.Set("Range", rangeValue)
		}
		if condition != "" {
			req.Header.Set("If-Range", condition)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		return response, body, readErr
	}
	documentKind := engineapi.ListSharedContentParamsType("documents")
	documents, err := client.ListSharedContentWithResponse(t.Context(), &engineapi.ListSharedContentParams{Type: &documentKind})
	if err != nil || documents.JSON200 == nil || len(documents.JSON200.Items) != 1 {
		t.Fatal("document fixture", err)
	}
	before := stream.calls.Load()
	notVideo, _, _ := request("GET", documents.JSON200.Items[0].Id, "", "")
	if notVideo.StatusCode != 404 || stream.calls.Load() != before {
		t.Fatal("non-video reference fetched", notVideo.StatusCode)
	}
	head, _, _ := request(http.MethodHead, string(first.ID), "", "")
	if head.StatusCode != 405 || head.Header.Get("Allow") != "GET" || stream.calls.Load() != before {
		t.Fatal("HEAD fetched provider", head.StatusCode)
	}
	for _, rangeValue := range []string{"bytes=1-2,4-5", "bytes=+1-2", "bytes=2-1", "bytes=-0", "bytes=-", "bytes=1--2", "items=0-1"} {
		res, _, _ := request("GET", string(first.ID), rangeValue, "")
		if res.StatusCode != 400 || stream.calls.Load() != before {
			t.Fatal("invalid range reached provider", rangeValue, res.StatusCode)
		}
	}
	for _, tt := range []struct {
		rangeValue, condition string
		status                int
		want, contentRange    string
	}{
		{status: 200, want: string(stream.data)},
		{rangeValue: "bytes=3-7", status: 206, want: "34567", contentRange: "bytes 3-7/36"},
		{rangeValue: "bytes=-3", status: 206, want: "xyz", contentRange: "bytes 33-35/36"},
		{rangeValue: "bytes=32-", condition: `"clip-v1"`, status: 206, want: "wxyz", contentRange: "bytes 32-35/36"},
		{rangeValue: "bytes=0-1", condition: `"changed"`, status: 200, want: string(stream.data)},
	} {
		res, body, err := request("GET", string(first.ID), tt.rangeValue, tt.condition)
		if err != nil || res.StatusCode != tt.status || string(body) != tt.want || res.Header.Get("Content-Range") != tt.contentRange || res.Header.Get("Content-Length") != strconv.Itoa(len(tt.want)) || res.Header.Get("Content-Type") != "video/mp4" || res.Header.Get("ETag") != `"clip-v1"` {
			t.Fatal("video range contract", res.StatusCode, string(body), res.Header, err)
		}
		select {
		case <-stream.closed:
		case <-time.After(time.Second):
			t.Fatal("stream body not closed")
		}
	}
	res, body, _ := request("GET", string(first.ID), "bytes=999-", "")
	var problem engineapi.Problem
	_ = json.Unmarshal(body, &problem)
	if res.StatusCode != 416 || res.Header.Get("Content-Range") != "bytes */36" || problem.Code != "range_not_satisfiable" {
		t.Fatal("unsatisfied range", res.StatusCode, string(body), res.Header)
	}
	before = stream.calls.Load()
	if err = e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "edit-source", OriginalText: "corrected source without a video", Historical: true, CreatedAt: time.Unix(2000, 0), Correction: &domain.MessageCorrection{Scope: "sender:peer", Author: "peer", ID: "edit-source", TargetID: "source-0"}}); err != nil {
		t.Fatal(err)
	}
	stale, _, _ := request("GET", scoped.JSON200.Items[0].Id, "", "")
	if stale.StatusCode != 404 || stream.calls.Load() != before {
		t.Fatal("corrected stale reference fetched", stale.StatusCode)
	}
	missing, _, _ := request("GET", string(id()), "", "")
	if missing.StatusCode != 404 || stream.calls.Load() != before {
		t.Fatal("missing reference fetched", missing.StatusCode)
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("playback changed outbox", err)
	}
}

type gatedMedia struct {
	first  chan struct{}
	closed chan struct{}
}
type gatedMediaBody struct {
	ctx    context.Context
	first  chan struct{}
	closed chan struct{}
	phase  int
	once   sync.Once
}

func (b *gatedMediaBody) Read(p []byte) (int, error) {
	if b.phase == 0 {
		select {
		case <-b.first:
			b.phase++
			return copy(p, "abc"), nil
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		}
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *gatedMediaBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }
func (g gatedMedia) Open(ctx context.Context, _ string, _ application.MediaReadRequest) (application.MediaRead, error) {
	return application.MediaRead{Body: &gatedMediaBody{ctx: ctx, first: g.first, closed: g.closed}, MediaType: "video/mp4", Length: 6, Total: 6}, nil
}
func TestSharedMediaHTTPFlushesBeforeBodyAndCancelsReader(t *testing.T) {
	e, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "gated-source", OriginalText: "https://files.example/gated.mp4", Historical: true}); err != nil {
		t.Fatal(err)
	}
	media := gatedMedia{first: make(chan struct{}), closed: make(chan struct{})}
	e.SetMediaReader(media)
	startMediaEngine(t, e)
	page := waitVideoCatalog(t, e)
	if len(page.Items) != 1 {
		t.Fatal(page)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/shared-content/"+string(page.Items[0].ID)+"/content", nil)
	type result struct {
		response *http.Response
		err      error
	}
	done := make(chan result, 1)
	go func() { response, err := http.DefaultClient.Do(request); done <- result{response, err} }()
	var response *http.Response
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		response = r.response
	case <-time.After(time.Second):
		t.Fatal("response headers waited for provider body")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 || response.Header.Get("Content-Length") != "6" || response.Header.Get("Content-Disposition") != "inline; filename=gated.mp4" {
		t.Fatal(response.StatusCode, response.Header)
	}
	close(media.first)
	first := make(chan string, 1)
	go func() {
		p := make([]byte, 3)
		_, err := io.ReadFull(response.Body, p)
		if err != nil {
			first <- err.Error()
			return
		}
		first <- string(p)
	}()
	select {
	case data := <-first:
		if data != "abc" {
			t.Fatal(data)
		}
	case <-time.After(time.Second):
		t.Fatal("first chunk waited for complete video")
	}
	cancel()
	_ = response.Body.Close()
	select {
	case <-media.closed:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not close engine source")
	}
}

func TestSharedDocumentHTTPPlainTextPDFRangesAndNoMutation(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	for i, ext := range []string{"txt", "log", "md", "csv", "json", "xml", "pdf", "docx"} {
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: fmt.Sprint("document-", i), OriginalText: "https://files.example/report." + ext, Historical: true, CreatedAt: time.Unix(1000+int64(i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	// Seed the prior parser's retained derived rows. The versioned worker must
	// reclassify new document formats while preserving each shared item UUID.
	priorIDs := map[string]domain.ID{}
	sources, err := e.Messages(t.Context(), c.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		refs := application.SharedReferences(source.OriginalText)
		for i := range refs {
			refs[i].ID = id()
			refs[i].RevisionID = id()
			priorIDs[refs[i].URL] = refs[i].ID
			if refs[i].FileName == "report.log" || refs[i].FileName == "report.json" || refs[i].FileName == "report.xml" {
				refs[i].Kind = "link"
			}
		}
		if err := store.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: source, References: refs, ParserVersion: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	stream := &streamedFixture{data: []byte("<script>alert('plain')</script>"), closed: make(chan struct{}, 32)}
	e.SetMediaReader(stream)
	startMediaEngine(t, e)
	var page domain.SharedContentPage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		page, err = e.SharedContent(t.Context(), domain.SharedContentQuery{Type: "documents", Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if page.Indexing.State == "complete" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(page.Items) != 8 || stream.calls.Load() != 0 {
		t.Fatal("catalog fetched document or omitted format", len(page.Items), stream.calls.Load())
	}
	for _, item := range page.Items {
		if item.ID != priorIDs[item.URL] {
			t.Fatal("reindex replaced retained catalog identity", item)
		}
		before := stream.calls.Load()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/shared-content/"+string(item.ID)+"/content", nil)
		req.Header.Set("Range", "bytes=0-7")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if item.FileName == "report.docx" {
			if response.StatusCode != 404 || stream.calls.Load() != before {
				t.Fatal("office fetched", response.StatusCode)
			}
			continue
		}
		want := "text/plain"
		if item.FileName == "report.pdf" {
			want = "application/pdf"
		}
		if err != nil || response.StatusCode != 206 || response.Header.Get("Content-Type") != want || response.Header.Get("X-Content-Type-Options") != "nosniff" || response.Header.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || string(body) != string(stream.data[:8]) || response.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-7/%d", len(stream.data)) {
			t.Fatal(response.StatusCode, response.Header, string(body), err)
		}
		before = stream.calls.Load()
		head, _ := http.NewRequestWithContext(t.Context(), http.MethodHead, req.URL.String(), nil)
		headResponse, err := http.DefaultClient.Do(head)
		if err != nil {
			t.Fatal(err)
		}
		_ = headResponse.Body.Close()
		if headResponse.StatusCode != 405 || headResponse.Header.Get("Allow") != "GET" || stream.calls.Load() != before {
			t.Fatal("HEAD reached provider")
		}
	}
	pending, err := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	if err != nil || len(pending) != 0 {
		t.Fatal("viewing mutated outbox", err)
	}
}
