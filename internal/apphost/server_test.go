package apphost

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/google/uuid"
)

func testServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := Start(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return s
}

func engineClient(t *testing.T, s *Server) *engineapi.ClientWithResponses {
	t.Helper()
	c, err := engineapi.NewClientWithResponses(s.URL(), engineapi.WithHTTPClient(&http.Client{Timeout: 2 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func engineIdentity(t *testing.T, s *Server) string {
	t.Helper()
	response, err := engineClient(t, s).GetEngineWithResponse(t.Context())
	if err != nil || response.JSON200 == nil {
		t.Fatalf("engine snapshot: %#v %v", response, err)
	}
	return response.JSON200.Id
}

func stream(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		_ = response.Body.Close()
		t.Fatalf("stream: %s %s", response.Status, response.Header.Get("Content-Type"))
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	first, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || !strings.HasPrefix(first, ": connected") {
		t.Fatalf("unflushed stream: %q %v", first, err)
	}
	return response
}

func TestCloseCancelsStreamsAndRestartPreservesIdentityAndAccounts(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Role: "local", Listen: "127.0.0.1:0", DataDir: dir, FFmpeg: filepath.Join(dir, "missing-ffmpeg")}
	first := testServer(t, cfg)
	identity := engineIdentity(t, first)
	client := engineClient(t, first)
	registered, err := client.RegisterAccountWithResponse(t.Context(), &engineapi.RegisterAccountParams{IdempotencyKey: uuid.Must(uuid.NewV7()).String()}, engineapi.RegisterAccount{Address: "alice@localhost", Label: "Retained account", Protocol: "xmpp"})
	if err != nil || registered.JSON201 == nil {
		t.Fatalf("save disconnected account: %#v %v", registered, err)
	}
	accountID := registered.JSON201.Id
	direct := stream(t, first.URL()+"/api/v1/events")
	// A native asset server owns this request rather than first.server. Its
	// request context must still stop before the database can close.
	presentation := httptest.NewServer(first.Handler())
	defer presentation.Close()
	external := stream(t, presentation.URL+"/ui/events")
	closed := make(chan error, 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			closed <- first.Close(ctx)
		}()
	}
	for range 2 {
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
	}
	if err := <-first.Done(); err != nil {
		t.Fatal(err)
	}
	for _, response := range []*http.Response{direct, external} {
		done := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, response.Body); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("stream survived runtime shutdown")
		}
	}
	// The exposed native handler rejects new requests after closure rather than
	// querying a closed store, even if its outer asset server is still alive.
	response, err := http.Get(presentation.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("closed native handler: %d", response.StatusCode)
	}
	second := testServer(t, cfg)
	if got := engineIdentity(t, second); got != identity {
		t.Fatalf("engine identity changed: %s != %s", got, identity)
	}
	accounts, err := engineClient(t, second).ListAccountsWithResponse(t.Context(), nil)
	if err != nil || accounts.JSON200 == nil || len(accounts.JSON200.Items) != 1 || accounts.JSON200.Items[0].Id != accountID {
		t.Fatalf("account lost after restart: %#v %v", accounts, err)
	}
}

func TestPrivateListenerRequiresCapabilityButNativePresentationWorks(t *testing.T) {
	s := testServer(t, Config{Listen: "127.0.0.1:0", DataDir: t.TempDir(), PrivateAPI: true})
	if s.capability == "" {
		t.Fatal("missing launch capability")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/", "/api/v1/engine", "/api/openapi.yaml", "/ui/events", "/assets/styles.css"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(capabilityHeader, "guessed")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden || strings.Contains(string(body), s.capability) {
			t.Fatalf("private listener exposed %s: %d", path, response.StatusCode)
		}
	}
	authorized, err := engineapi.NewClientWithResponses(s.URL(), engineapi.WithHTTPClient(client), engineapi.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
		request.Header.Set(capabilityHeader, s.capability)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := authorized.GetEngineWithResponse(t.Context())
	if err != nil || engine.JSON200 == nil {
		t.Fatalf("private engine request: %#v %v", engine, err)
	}
	presentation := httptest.NewServer(s.Handler())
	defer presentation.Close()
	response, err := client.Get(presentation.URL + "/?lang=en")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), engine.JSON200.Id) {
		t.Fatalf("private BFF cannot reach own engine: %d %v", response.StatusCode, err)
	}
	if strings.Contains(string(body), s.capability) || strings.Contains(string(body), capabilityHeader) {
		t.Fatal("launch capability leaked into HTML")
	}
	for _, path := range []string{"/api/v1/engine", "/api/openapi.yaml"} {
		response, err = client.Get(presentation.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("native handler exposed structured API %s: %d", path, response.StatusCode)
		}
	}
	// BFF live events need the capability too, but never expose it to the WebView.
	events := stream(t, presentation.URL+"/ui/events")
	defer func() { _ = events.Body.Close() }()
}

func TestNativePresentationKeepsValidatedBFFJSONRoutes(t *testing.T) {
	engineID, accountID, conversationID := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	previewID, itemID, messageID := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	revisionID := uuid.Must(uuid.NewV7()).String()
	readPath := "/api/v1/conversations/" + conversationID + "/read-state"
	previewPath := "/api/v1/previews/" + previewID
	thumbnailPath := "/api/v1/shared-content/" + itemID + "/thumbnail"
	var calls, writes atomic.Int32
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		var body any
		switch r.URL.Path {
		case "/api/v1/engine":
			body = map[string]any{"id": engineID}
		case "/api/v1/conversations/" + conversationID:
			body = engineapi.Conversation{Id: conversationID, AccountId: accountID}
		case readPath:
			unread := 2
			if r.Method == http.MethodPost {
				var mark engineapi.MarkMessagesRead
				if err := json.NewDecoder(r.Body).Decode(&mark); err != nil || len(mark.MessageIds) != 1 || mark.MessageIds[0] != messageID || r.Header.Get("Idempotency-Key") != revisionID {
					t.Error("BFF did not preserve the validated read-state operation")
				}
				writes.Add(1)
				unread = 0
			}
			body = engineapi.ConversationReadState{ConversationId: conversationID, AccountId: accountID, UnreadCount: unread}
		case previewPath:
			body = engineapi.MessagePreview{Id: previewID, RevisionId: revisionID, AccountId: accountID, ConversationId: conversationID, MessageId: messageID, Kind: "image", State: "ready", DownloadState: "queued", Url: "https://example.test/image.jpg", UpdatedAt: time.Now().UTC()}
		case thumbnailPath:
			body = engineapi.VideoThumbnail{Id: previewID, RevisionId: revisionID, SharedContentId: itemID, AccountId: accountID, ConversationId: conversationID, MessageId: messageID, Kind: "video", State: "queued", Url: "https://example.test/video.mp4", UpdatedAt: time.Now().UTC()}
		default:
			t.Errorf("native presentation forwarded an unexpected engine path: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	}))
	defer engine.Close()
	s := testServer(t, Config{Role: "bff", Listen: "127.0.0.1:0", EngineURL: engine.URL, PrivateAPI: true})
	csrf := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request := func(method, path, token string) *httptest.ResponseRecorder {
		t.Helper()
		body := `{"messageIds":["` + messageID + `"]}`
		r := httptest.NewRequest(method, "wails://localhost"+path, strings.NewReader(body))
		r.Header.Set("X-Oie-Engine-ID", engineID)
		r.Header.Set("X-Oie-Account-ID", accountID)
		r.Header.Set("Idempotency-Key", revisionID)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", token)
		r.AddCookie(&http.Cookie{Name: "oie_csrf", Value: csrf})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{previewPath, thumbnailPath, readPath} {
		w := request(http.MethodGet, path, "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("native BFF query %s failed: %d %s", path, w.Code, w.Body)
		}
	}
	before := calls.Load()
	if w := request(http.MethodPost, readPath, "forged"); w.Code != 403 || calls.Load() != before || writes.Load() != 0 {
		t.Fatalf("forged CSRF reached engine: status=%d calls=%d writes=%d", w.Code, calls.Load(), writes.Load())
	}
	w := request(http.MethodPost, readPath, csrf)
	var marked engineapi.ConversationReadState
	if err := json.Unmarshal(w.Body.Bytes(), &marked); err != nil || w.Code != 200 || marked.UnreadCount != 0 || writes.Load() != 1 {
		t.Fatalf("valid native read-state mutation failed: %d %s %v", w.Code, w.Body, err)
	}
	if w.Header().Get("X-Oie-Next-Operation-Key") == "" {
		t.Fatal("native mutation bypassed the BFF operation response")
	}
	before = calls.Load()
	for _, scenario := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/engine"},
		{http.MethodGet, "/api/openapi.yaml"},
		{http.MethodGet, "/api/v1/accounts"},
		{http.MethodGet, "/api/v1/conversations/" + conversationID + "/messages"},
		{http.MethodPost, previewPath + "/download"},
		{http.MethodPost, thumbnailPath},
		{http.MethodDelete, readPath},
		{http.MethodGet, previewPath + "/extra"},
	} {
		w = request(scenario.method, scenario.path, csrf)
		if w.Code != 404 || calls.Load() != before {
			t.Fatalf("native handler exposed raw API %s %s: status=%d", scenario.method, scenario.path, w.Code)
		}
	}
}

func TestFailedStartupReleasesListenerAndStore(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "invalid-ca.pem")
	if err := os.WriteFile(ca, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	bound, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := bound.Addr().String()
	if err = bound.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Start(t.Context(), Config{Listen: address, DataDir: dir, XMPPCAFile: ca}); err == nil {
		t.Fatal("accepted invalid CA")
	}
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("failed startup retained listener: %v", err)
	}
	_ = rebound.Close()
	candidate, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(t.Context(), dir, candidate)
	if err != nil {
		t.Fatalf("failed startup retained data lock: %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	_ = testServer(t, Config{Listen: address, DataDir: dir})
}

func TestBFFOnlyAndCancelledStartupDoNotOpenLocalData(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unused")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Start(ctx, Config{Listen: "127.0.0.1:0", DataDir: dir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup: %v", err)
	}
	if _, err := Start(t.Context(), Config{Role: "bff", Listen: "127.0.0.1:0", DataDir: dir, EngineURL: ":invalid"}); err == nil {
		t.Fatal("accepted invalid BFF engine URL")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("presentation startup touched local data: %v", err)
	}
	engine := testServer(t, Config{Listen: "127.0.0.1:0", DataDir: t.TempDir()})
	bff := testServer(t, Config{Role: "bff", Listen: "127.0.0.1:0", EngineURL: engine.URL(), DataDir: dir})
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("BFF role created data store: %v", err)
	}
	// Parent cancellation alone owns cleanup; Close is a wait, not a required
	// trigger to release the store and listener.
	parent, stop := context.WithCancel(t.Context())
	child, err := Start(parent, Config{Listen: "127.0.0.1:0", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	select {
	case err := <-child.Done():
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent cancellation did not close runtime")
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bff.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
}

func TestFailedProtocolStartupReleasesDataLock(t *testing.T) {
	dir := t.TempDir()
	connections := filepath.Join(dir, "private", "connections")
	if err := os.MkdirAll(connections, 0700); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(connections, "invalid.json")
	if err := os.WriteFile(invalid, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Listen: "127.0.0.1:0", DataDir: dir}
	if _, err := Start(t.Context(), cfg); err == nil {
		t.Fatal("accepted broken retained connection configuration")
	}
	if err := os.Remove(invalid); err != nil {
		t.Fatal(err)
	}
	_ = testServer(t, cfg)
}

func TestCloseDeadlineKeepsStoreOwnedUntilNativeRequestFinishes(t *testing.T) {
	dir := t.TempDir()
	s := testServer(t, Config{Listen: "127.0.0.1:0", DataDir: dir})
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	native := httptest.NewServer(s.track(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})))
	// Defers run before t.Cleanup; release the external request before its host
	// waits for handlers, including on an assertion failure.
	defer native.Close()
	defer unblock()
	response := make(chan error, 1)
	go func() {
		res, err := http.Get(native.URL)
		if err == nil {
			_ = res.Body.Close()
		}
		response <- err
	}()
	<-entered
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Close(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close ignored caller deadline: %v", err)
	}
	<-cancelled
	candidate, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	extra, err := sqlite.Open(t.Context(), dir, candidate)
	if err == nil {
		_ = extra.Close()
		t.Fatal("a timed-out close released storage under an active native request")
	}
	unblock()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-response; err != nil {
		t.Fatal(err)
	}
	extra, err = sqlite.Open(t.Context(), dir, candidate)
	if err != nil {
		t.Fatalf("completed native request did not release storage: %v", err)
	}
	if err = extra.Close(); err != nil {
		t.Fatal(err)
	}
}
