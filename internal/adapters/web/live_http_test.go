package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

const testEngine = "019a5df0-0000-7000-8000-000000000001"
const testAccount = "019a5df0-0000-7000-8000-000000000002"
const testChat = "019a5df0-0000-7000-8000-000000000003"
const testMessage = "019a5df0-0000-7000-8000-000000000004"
const testRevision = "019a5df0-0000-7000-8000-000000000005"

// HTTP API fixture owns effects independently of the rendered UI. Response loss
// happens only after an acceptance is committed, then receipt reconciliation is real HTTP.
type liveFixture struct {
	mu                         sync.Mutex
	effects, connects, cancels int
	calls                      int
	engineUnavailable          bool
	acceptedKey, acceptedBody  string
	dropReply                  bool
	state                      string
	lastPassword               string
}

func (f *liveFixture) message() engineapi.Message {
	return engineapi.Message{Id: testMessage, RevisionId: testRevision, AccountId: testAccount, ConversationId: testChat, Sender: engineapi.Sender{Id: testAccount, DisplayName: "Alice"}, OriginalText: f.acceptedBody, CreatedAt: time.Date(2026, 11, 7, 18, 0, 0, 0, time.UTC), IsOwn: true, SendState: engineapi.MessageSendState(f.state)}
}
func (f *liveFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.URL.Path == "/api/v1/engine":
		if f.engineUnavailable {
			w.WriteHeader(503)
			write(map[string]any{"code": "unavailable", "status": 503})
			return
		}
		write(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.2.0", "eventCursor": testEngine + ":0"})
	case r.URL.Path == "/api/v1/accounts":
		write(map[string]any{"items": []any{map[string]any{"id": testAccount, "revisionId": testRevision, "protocol": "xmpp", "address": "alice@localhost", "label": "Alice", "createdAt": "2026-11-07T18:00:00Z", "connectionState": "not_connected"}}})
	case r.URL.Path == "/api/v1/accounts/"+testAccount+"/connection" && r.Method == "PUT":
		f.connects++
		var input engineapi.ConfigureConnection
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Password != nil {
			f.lastPassword = *input.Password
		}
		w.WriteHeader(202)
		write(engineapi.Connection{AccountId: testAccount, State: "connecting", UpdatedAt: time.Now().UTC()})
	case r.URL.Path == "/api/v1/accounts/"+testAccount+"/connection":
		write(engineapi.Connection{AccountId: testAccount, State: "not_connected", UpdatedAt: time.Now().UTC()})
	case r.URL.Path == "/api/v1/conversations":
		write(map[string]any{"items": []any{map[string]any{"id": testChat, "revisionId": testRevision, "accountId": testAccount, "kind": "direct", "address": "bob@localhost", "title": "Bob", "lastMessage": "", "updatedAt": "2026-11-07T18:00:00Z", "unreadCount": 0, "archived": false, "favorite": false}}})
	case r.URL.Path == "/api/v1/conversations/"+testChat:
		write(engineapi.Conversation{Id: testChat, RevisionId: testRevision, AccountId: testAccount, Address: "bob@localhost", Title: "Bob", Kind: "direct", UpdatedAt: time.Now().UTC()})
	case strings.HasPrefix(r.URL.Path, "/api/v1/operations/"):
		if f.acceptedKey != "" && strings.TrimPrefix(r.URL.Path, "/api/v1/operations/") == f.acceptedKey {
			write(engineapi.Operation{Id: f.acceptedKey, Kind: "send_message", ResourceId: testMessage, Status: "succeeded", CreatedAt: time.Now().UTC()})
		} else {
			w.WriteHeader(404)
			write(map[string]any{"code": "not_found", "status": 404})
		}
	case r.URL.Path == "/api/v1/conversations/"+testChat+"/messages" && r.Method == "POST":
		key := r.Header.Get("Idempotency-Key")
		var input engineapi.SendMessage
		_ = json.NewDecoder(r.Body).Decode(&input)
		if f.acceptedKey == "" {
			f.effects++
			f.acceptedKey = key
			f.acceptedBody = input.OriginalText
			f.state = "queued"
		}
		if f.dropReply {
			f.dropReply = false
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(202)
		write(f.message())
	case r.URL.Path == "/api/v1/conversations/"+testChat+"/messages":
		items := []engineapi.Message{}
		if f.acceptedKey != "" {
			items = append(items, f.message())
		}
		write(engineapi.MessagePage{Items: items})
	case r.URL.Path == "/api/v1/messages/"+testMessage:
		write(f.message())
	case r.URL.Path == "/api/v1/messages/"+testMessage+"/cancel":
		f.cancels++
		w.WriteHeader(409)
		m := f.message()
		m.SendState = "submitted"
		write(engineapi.Problem{Type: "urn:oie:problem:state_conflict", Status: 409, Code: "state_conflict", Title: "Conflict", CurrentMessage: &m})
	default:
		w.WriteHeader(404)
		write(map[string]any{"code": "not_found", "status": 404})
	}
}
func liveUI(t *testing.T, f *liveFixture) (*httptest.Server, *http.Client, string) {
	t.Helper()
	backend := httptest.NewServer(f)
	t.Cleanup(backend.Close)
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(ui)
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	response, err := client.Get(server.URL + "/?chat=" + testChat)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	u, _ := url.Parse(server.URL)
	token := ""
	for _, c := range jar.Cookies(u) {
		if c.Name == csrfCookie {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("CSRF cookie not established")
	}
	return server, client, token
}
func livePost(t *testing.T, client *http.Client, base, path, csrf string, values url.Values) (int, mutationResult) {
	t.Helper()
	values.Set("engineId", testEngine)
	values.Set("csrf", csrf)
	req, _ := http.NewRequest("POST", base+path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var result mutationResult
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, result
}
func TestLiveCSRFAndLostAcceptanceKeepOneEffect(t *testing.T) {
	f := &liveFixture{dropReply: true}
	ui, client, csrf := liveUI(t, f)
	key := newOperationKey()
	values := url.Values{"operationKey": {key}, "accountId": {testAccount}, "conversationId": {testChat}, "originalText": {"Hello Bob 👋\nA real original body <&>"}}
	status, _ := livePost(t, client, ui.URL, "/ui/messages/send", "forged", values)
	if status != 403 || f.effectCount() != 0 {
		t.Fatal("forged CSRF changed engine state")
	}
	status, result := livePost(t, client, ui.URL, "/ui/messages/send", csrf, values)
	if status != 200 || !result.Accepted || result.MessageID != testMessage || f.effectCount() != 1 {
		t.Fatalf("lost response not reconciled: %d %+v effects=%d", status, result, f.effectCount())
	}
	status, result = livePost(t, client, ui.URL, "/ui/messages/send", csrf, values)
	if status != 200 || !result.Accepted || f.effectCount() != 1 {
		t.Fatal("repeated request created another effect")
	}
	values.Set("originalText", "different content")
	status, result = livePost(t, client, ui.URL, "/ui/messages/send", csrf, values)
	if status != 409 || result.Accepted || f.effectCount() != 1 {
		t.Fatal("changed body with same key was accepted")
	}
}
func TestLiveCancelConflictAndPasswordNeverEcho(t *testing.T) {
	f := &liveFixture{acceptedBody: "Hello", state: "submitted"}
	ui, client, csrf := liveUI(t, f)
	status, result := livePost(t, client, ui.URL, "/ui/messages/cancel", csrf, url.Values{"operationKey": {newOperationKey()}, "messageId": {testMessage}, "expectedRevisionId": {testRevision}})
	if status != 409 || result.OK || result.State != "submitted" || !strings.Contains(result.Message, "Submitted") {
		t.Fatalf("cancel falsely succeeded: %d %+v", status, result)
	}
	password := "private-password-do-not-echo"
	status, result = livePost(t, client, ui.URL, "/ui/accounts/connect", csrf, url.Values{"accountId": {testAccount}, "password": {password}, "endpoint": {"127.0.0.1:5222"}})
	data, _ := json.Marshal(result)
	if status != 200 || f.passwordSeen() != password || strings.Contains(string(data), password) {
		t.Fatal("password did not stay write-only")
	}
	values := url.Values{"csrf": {csrf}, "engineId": {testEngine}, "accountId": {testAccount}, "password": {password}, "returnTo": {"/"}}
	// Invalid CSRF creates a real native HTML recovery page; no password readback.
	values.Set("csrf", "invalid")
	res, err := client.PostForm(ui.URL+"/ui/accounts/connect", values)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 403 || strings.Contains(string(body), password) {
		t.Fatal("native error echoed a password")
	}
}
func TestLiveSSEForwardsCursorAndOutlivesOrdinaryClientTimeout(t *testing.T) {
	seen := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": upstream\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5500 * time.Millisecond):
			_, _ = io.WriteString(w, "id: "+testEngine+":2\nevent: message.created\ndata: {}\n\n")
			w.(http.Flusher).Flush()
		}
	}))
	defer backend.Close()
	ui, _ := New(backend.URL)
	server := httptest.NewServer(ui)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/ui/events?cursor="+testEngine+":0", nil)
	req.Header.Set("Last-Event-ID", testEngine+":1")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := <-seen; got != testEngine+":1" {
		t.Fatalf("reconnect cursor lost: %s", got)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(data), "message.created") {
		t.Fatalf("stream ended at normal timeout: %s %v", data, err)
	}
}

func (f *liveFixture) effectCount() int     { f.mu.Lock(); defer f.mu.Unlock(); return f.effects }
func (f *liveFixture) passwordSeen() string { f.mu.Lock(); defer f.mu.Unlock(); return f.lastPassword }

func TestLiveNativePRGAndPostOnlyGuard(t *testing.T) {
	fixture := &liveFixture{}
	ui, client, csrf := liveUI(t, fixture)
	get, err := client.Get(ui.URL + "/ui/messages/send")
	if err != nil {
		t.Fatal(err)
	}
	_ = get.Body.Close()
	if get.StatusCode != 405 || fixture.effectCount() != 0 {
		t.Fatal("GET performed a mutation")
	}
	values := url.Values{"csrf": {csrf}, "engineId": {testEngine}, "operationKey": {newOperationKey()}, "conversationId": {testChat}, "accountId": {testAccount}, "originalText": {"Native send"}, "returnTo": {"/?chat=" + testChat}}
	response, err := client.PostForm(ui.URL+"/ui/messages/send", values)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.Request.Method != "GET" || response.Request.URL.Query().Get("chat") != testChat || fixture.effectCount() != 1 {
		t.Fatal("native success did not use PRG to the actual conversation")
	}
	refreshed, err := client.Get(response.Request.URL.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = refreshed.Body.Close()
	if fixture.effectCount() != 1 {
		t.Fatal("refresh repeated native submission")
	}
}

func (f *liveFixture) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }
func TestEveryMutationRequiresCSRFBeforeBackendAccess(t *testing.T) {
	fixture := &liveFixture{}
	ui, client, _ := liveUI(t, fixture)
	before := fixture.callCount()
	for _, path := range []string{"/ui/accounts/register", "/ui/accounts/connect", "/ui/accounts/disconnect", "/ui/conversations/open", "/ui/messages/send", "/ui/messages/cancel"} {
		status, _ := livePost(t, client, ui.URL, path, "forged", url.Values{"password": {"never-echo-secret"}})
		if status != 403 {
			t.Errorf("%s accepted forged CSRF", path)
		}
		response, err := client.Get(ui.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 405 {
			t.Errorf("%s allows non-POST", path)
		}
	}
	if fixture.callCount() != before {
		t.Fatal("forged actions reached backend")
	}
}
func TestNativeUnavailableSendRetainsOriginalAndOperationKey(t *testing.T) {
	fixture := &liveFixture{}
	ui, client, csrf := liveUI(t, fixture)
	fixture.mu.Lock()
	fixture.engineUnavailable = true
	fixture.mu.Unlock()
	key := newOperationKey()
	body := "Retain this native draft"
	values := url.Values{"csrf": {csrf}, "engineId": {testEngine}, "operationKey": {key}, "conversationId": {testChat}, "accountId": {testAccount}, "originalText": {body}, "returnTo": {"/?chat=" + testChat}, "formTarget": {"send"}}
	response, err := client.PostForm(ui.URL+"/ui/messages/send", values)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	html, _ := io.ReadAll(response.Body)
	if response.StatusCode != 503 || !strings.Contains(string(html), body) || !strings.Contains(string(html), key) || !strings.Contains(string(html), "native-retained-message") || fixture.effectCount() != 0 {
		t.Fatalf("native draft recovery lost context: %d", response.StatusCode)
	}
}

func TestNativeAccountRecoveryReturnsToItsSingleScreen(t *testing.T) {
	fixture := &liveFixture{}
	ui, client, csrf := liveUI(t, fixture)
	values := url.Values{"csrf": {csrf}, "engineId": {testEngine}, "accountId": {testAccount}, "endpoint": {"127.0.0.1:5222"}, "formTarget": {"connect-" + testAccount}, "returnTo": {"/"}}
	response, err := client.PostForm(ui.URL+"/ui/accounts/connect", values)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	html := string(data)
	if response.StatusCode != 422 || strings.Count(html, "data-connect-account") != 1 || !strings.Contains(html, `data-open-drawer="accounts"`) || !strings.Contains(html, `value="127.0.0.1:5222"`) {
		t.Fatal("connection validation did not retain its detail screen")
	}
	fixture.mu.Lock()
	fixture.engineUnavailable = true
	fixture.mu.Unlock()
	key := newOperationKey()
	values = url.Values{"csrf": {csrf}, "engineId": {testEngine}, "operationKey": {key}, "address": {"carol@localhost"}, "label": {"Carol"}, "formTarget": {"register"}, "returnTo": {"/"}}
	response, err = client.PostForm(ui.URL+"/ui/accounts/register", values)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	html = string(data)
	if response.StatusCode != 503 || !strings.Contains(html, `id="register-account-form"`) || strings.Contains(html, "data-connect-account") || !strings.Contains(html, key) || !strings.Contains(html, "carol@localhost") || !strings.Contains(html, `data-open-drawer="accounts"`) {
		t.Fatal("unavailable registration lost its screen, values or logical operation key")
	}
}
