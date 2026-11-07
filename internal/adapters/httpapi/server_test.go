package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func id() domain.ID { return domain.ID(uuid.Must(uuid.NewV7()).String()) }

func start(t *testing.T, dir string) (*httptest.Server, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), dir, id())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := application.New(store, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() { server.Close(); _ = store.Close() })
	return server, store
}

func request(t *testing.T, server, method, path, key, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, server+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode >= 400 && res.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("non-contract error response: %s %s", res.Header.Get("Content-Type"), data)
	}
	return res.StatusCode, data
}

func register(t *testing.T, server, key, address string) engineapi.Account {
	t.Helper()
	status, data := request(t, server, "POST", "/api/v1/accounts", key,
		fmt.Sprintf(`{"protocol":"xmpp","address":%q,"label":"Personal"}`, address))
	if status != 201 && status != 200 {
		t.Fatalf("register: %d %s", status, data)
	}
	var out engineapi.Account
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRegistrationIdempotencyAndTransactionalConflict(t *testing.T) {
	server, store := start(t, t.TempDir())
	key := string(id())
	a := register(t, server.URL, key, "alice@localhost")
	status, data := request(t, server.URL, "POST", "/api/v1/accounts", key,
		`{"protocol":"xmpp","address":" alice@localhost ","label":" Personal "}`)
	if status != 200 {
		t.Fatalf("expected original receipt: %d %s", status, data)
	}
	var replay engineapi.Account
	_ = json.Unmarshal(data, &replay)
	if replay.Id != a.Id || replay.RevisionId != a.RevisionId {
		t.Fatal("replay changed identity")
	}
	status, _ = request(t, server.URL, "POST", "/api/v1/accounts", key,
		`{"protocol":"xmpp","address":"alice@localhost","label":"Changed"}`)
	if status != 409 {
		t.Fatalf("payload conflict = %d", status)
	}
	duplicateKey := string(id())
	status, _ = request(t, server.URL, "POST", "/api/v1/accounts", duplicateKey,
		`{"protocol":"xmpp","address":"alice@localhost","label":"Other"}`)
	if status != 409 {
		t.Fatalf("duplicate address = %d", status)
	}
	status, _ = request(t, server.URL, "GET", "/api/v1/operations/"+duplicateKey, "", "")
	if status != 404 {
		t.Fatalf("rolled-back registration left a receipt: %d", status)
	}
	_, seq, err := store.Snapshot(t.Context())
	if err != nil || seq != 1 {
		t.Fatalf("duplicate side effect: seq=%d err=%v", seq, err)
	}
	status, data = request(t, server.URL, "GET", "/api/v1/operations/"+key, "", "")
	if status != 200 || !bytes.Contains(data, []byte(a.Id)) {
		t.Fatalf("cannot reconcile: %d %s", status, data)
	}
	status, data = request(t, server.URL, "GET", "/api/v1/accounts/"+a.Id+"/capabilities", "", "")
	if status != 200 || !bytes.Contains(data, []byte(`"state":"unknown"`)) {
		t.Fatalf("capability overclaim: %s", data)
	}
}

func TestConcurrentRetriesHaveOneEffect(t *testing.T) {
	server, store := start(t, t.TempDir())
	key := string(id())
	var wg sync.WaitGroup
	statuses := make(chan int, 12)
	for range 12 {
		wg.Go(func() {
			req, _ := http.NewRequest("POST", server.URL+"/api/v1/accounts", strings.NewReader(`{"protocol":"xmpp","address":"bob@localhost","label":"Bob"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				statuses <- 0
				return
			}
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			statuses <- res.StatusCode
		})
	}
	wg.Wait()
	close(statuses)
	created := 0
	for status := range statuses {
		if status == 201 {
			created++
		} else if status != 200 {
			t.Fatalf("retry status %d", status)
		}
	}
	_, seq, err := store.Snapshot(t.Context())
	if created != 1 || seq != 1 || err != nil {
		t.Fatalf("created=%d seq=%d err=%v", created, seq, err)
	}
}

func TestValidationAndPagination(t *testing.T) {
	server, store := start(t, t.TempDir())
	for _, body := range []string{
		`{"protocol":"xmpp","address":"alice@localhost"}`,
		`{"protocol":"matrix","address":"alice","label":"Alice"}`,
		`{"protocol":"xmpp","address":"alice","label":"Alice","password":"must-not-be-accepted"}`,
		`{"protocol":"xmpp","address":" ","label":"Alice"}`,
	} {
		status, _ := request(t, server.URL, "POST", "/api/v1/accounts", string(id()), body)
		if status != 400 {
			t.Fatalf("invalid payload accepted: %d", status)
		}
	}
	status, _ := request(t, server.URL, "POST", "/api/v1/accounts", uuid.NewString(),
		`{"protocol":"xmpp","address":"alice","label":"Alice"}`)
	if status != 400 {
		t.Fatal("UUIDv4 accepted as operation ID")
	}
	_, seq, _ := store.Snapshot(t.Context())
	if seq != 0 {
		t.Fatal("invalid requests changed state")
	}
	register(t, server.URL, string(id()), "alice@localhost")
	register(t, server.URL, string(id()), "bob@localhost")
	status, data := request(t, server.URL, "GET", "/api/v1/accounts?limit=1", "", "")
	var first engineapi.AccountPage
	_ = json.Unmarshal(data, &first)
	if status != 200 || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page: %s", data)
	}
	status, data = request(t, server.URL, "GET", "/api/v1/accounts?limit=1&after="+*first.NextCursor, "", "")
	var second engineapi.AccountPage
	_ = json.Unmarshal(data, &second)
	if status != 200 || len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].Id == first.Items[0].Id {
		t.Fatalf("second page: %s", data)
	}
	status, _ = request(t, server.URL, "GET", "/api/v1/accounts?limit=0", "", "")
	if status != 400 {
		t.Fatal("invalid limit accepted")
	}
}

func TestRestartPreservesIdentityReceiptAndEvents(t *testing.T) {
	dir := t.TempDir()
	server, store := start(t, dir)
	key := string(id())
	a := register(t, server.URL, key, "alice@localhost")
	engineID, _, _ := store.Snapshot(t.Context())
	server.Close()
	_ = store.Close()
	restarted, again := start(t, dir)
	replayed := register(t, restarted.URL, key, "alice@localhost")
	gotID, seq, err := again.Snapshot(t.Context())
	if err != nil || gotID != engineID || seq != 1 || replayed.Id != a.Id {
		t.Fatal("restart lost durable identity/receipt")
	}
	events, err := again.Events(t.Context(), 0, 100)
	if err != nil || len(events) != 1 || string(events[0].Account.ID) != a.Id {
		t.Fatal("restart lost event")
	}
}

func TestSSEReplayAndCursorValidation(t *testing.T) {
	server, store := start(t, t.TempDir())
	register(t, server.URL, string(id()), "alice@localhost")
	engineID, seq, _ := store.Snapshot(t.Context())
	a := register(t, server.URL, string(id()), "bob@localhost")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events", nil)
	req.Header.Set("Last-Event-ID", fmt.Sprintf("%s:%d", engineID, seq))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE status=%d", res.StatusCode)
	}
	scanner := bufio.NewScanner(res.Body)
	seenID := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			seenID = strings.TrimPrefix(line, "id: ")
		}
		if strings.HasPrefix(line, "data: ") {
			var e engineapi.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
				t.Fatal(err)
			}
			if e.Account.Id != a.Id || e.Id != seenID || e.Id != fmt.Sprintf("%s:2", engineID) {
				t.Fatalf("bad replay: %+v", e)
			}
			break
		}
	}
	if seenID == "" {
		t.Fatalf("no replay event: %v", scanner.Err())
	}
	cancel()
	for _, cursor := range []string{string(id()) + ":0", string(engineID) + ":999", string(engineID) + ":-1"} {
		req, _ := http.NewRequest("GET", server.URL+"/api/v1/events", nil)
		req.Header.Set("Last-Event-ID", cursor)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 409 {
			t.Fatalf("invalid cursor produced %d", res.StatusCode)
		}
	}
}

func TestUnavailableReturnsStructuredProblem(t *testing.T) {
	server, store := start(t, t.TempDir())
	_ = store.Close()
	status, data := request(t, server.URL, "GET", "/api/v1/health", "", "")
	if status != 503 || !bytes.Contains(data, []byte(`"code":"unavailable"`)) || bytes.Contains(data, []byte("sql:")) {
		t.Fatalf("unavailable leaked or wrong status: %d %s", status, data)
	}
}

func TestUnknownRouteAndWrongMethod(t *testing.T) {
	server, _ := start(t, t.TempDir())
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/missing", 404},
		{"DELETE", "/api/v1/health", 405},
	} {
		status, _ := request(t, server.URL, tc.method, tc.path, "", "")
		if status != tc.status {
			t.Fatalf("%s %s returned %d", tc.method, tc.path, status)
		}
	}
}

// lostReply drops the socket only after the use case committed and the generated
// response visitor attempts to send its success response.
type lostReply struct {
	http.ResponseWriter
	dropped bool
}

func (w *lostReply) WriteHeader(_ int) {
	if w.dropped {
		return
	}
	w.dropped = true
	conn, _, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func (w *lostReply) Write(_ []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLostSuccessResponseReconcilesWithoutAnotherEffect(t *testing.T) {
	server, store := start(t, t.TempDir())
	var drop atomic.Bool
	fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && drop.CompareAndSwap(false, true) {
			server.Config.Handler.ServeHTTP(&lostReply{ResponseWriter: w}, r)
			return
		}
		server.Config.Handler.ServeHTTP(w, r)
	}))
	defer fault.Close()
	key := string(id())
	body := `{"protocol":"xmpp","address":"lost@localhost","label":"Lost reply"}`
	req, _ := http.NewRequest("POST", fault.URL+"/api/v1/accounts", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if res != nil {
		_ = res.Body.Close()
	}
	if err == nil {
		t.Fatal("injected response loss did not occur")
	}
	status, data := request(t, fault.URL, "GET", "/api/v1/operations/"+key, "", "")
	if status != 200 {
		t.Fatalf("committed receipt unavailable: %d %s", status, data)
	}
	var operation engineapi.Operation
	if err := json.Unmarshal(data, &operation); err != nil {
		t.Fatal(err)
	}
	status, data = request(t, fault.URL, "POST", "/api/v1/accounts", key, body)
	if status != 200 || !bytes.Contains(data, []byte(operation.ResourceId)) {
		t.Fatalf("retry did not recover original result: %d %s", status, data)
	}
	_, sequence, err := store.Snapshot(t.Context())
	if err != nil || sequence != 1 {
		t.Fatalf("duplicate effect after lost response: %d %v", sequence, err)
	}
}
