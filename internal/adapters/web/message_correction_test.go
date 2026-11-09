package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestBrowserCorrectionScopedReceiptAndReplay(t *testing.T) {
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	target := engineapi.MessageEditTarget{MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, CurrentRevisionId: testRevision, OriginalText: "Existing text", Eligible: true}
	commands := map[string]engineapi.MessageEditCommand{}
	writes, receiptReads, targetReads := 0, 0, 0
	conflict := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.25.0", "eventCursor": testEngine + ":0"})
		case "/api/v1/conversations/" + testChat + "/edit-target", "/api/v1/messages/" + testMessage + "/edit-target":
			targetReads++
			_ = json.NewEncoder(w).Encode(target)
		case "/api/v1/messages/" + testMessage + "/edits":
			writes++
			var input engineapi.EditMessage
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if conflict {
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"code":"state_conflict"}`))
				return
			}
			command := engineapi.MessageEditCommand{Id: r.Header.Get("Idempotency-Key"), RevisionId: testRevision, MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, ExpectedRevisionId: input.ExpectedRevisionId, OriginalText: input.OriginalText, State: "submitted", CreatedAt: at, UpdatedAt: at}
			commands[command.Id] = command
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(command)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/v1/message-edits/") {
				receiptReads++
				command, ok := commands[strings.TrimPrefix(r.URL.Path, "/api/v1/message-edits/")]
				if !ok {
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"code":"not_found"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(command)
				return
			}
			t.Errorf("unrelated correction request %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer backend.Close()
	handler, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	ui := httptest.NewServer(handler)
	defer ui.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, e := client.Get(ui.URL + "/ui/message-edit-target?engineId=" + testEngine + "&accountId=" + testAccount + "&conversationId=" + testChat)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	csrf := ""
	for _, cookie := range jar.Cookies(resp.Request.URL) {
		if cookie.Name == csrfCookie {
			csrf = cookie.Value
		}
	}
	if csrf == "" {
		t.Fatal("missing CSRF token")
	}
	targetReads = 0
	q := url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "lang": {"en"}}
	req, _ := http.NewRequest("GET", ui.URL+"/ui/message-edit-target?"+q.Encode(), nil)
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var prepared editPreparation
	_ = json.NewDecoder(response.Body).Decode(&prepared)
	_ = response.Body.Close()
	if response.StatusCode != 200 || prepared.Target.MessageId != testMessage || !validOperationKey(prepared.OperationKey) || targetReads != 1 {
		t.Fatalf("latest lookup failed: status%d %+v", response.StatusCode, prepared)
	}
	values := url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "messageId": {testMessage}, "expectedRevisionId": {testRevision}, "operationKey": {prepared.OperationKey}, "originalText": {"Corrected **literal** 😀"}, "lang": {"pt-BR"}}
	post := func(path, token string, form url.Values) (int, editResult) {
		t.Helper()
		r, _ := http.NewRequest("POST", ui.URL+path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Accept", "application/json")
		r.Header.Set("X-CSRF-Token", token)
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = resp.Body.Close() }()
		var result editResult
		if e := json.NewDecoder(resp.Body).Decode(&result); e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, result
	}
	status, result := post("/ui/messages/edit-receipt", csrf, values)
	if status != 404 || result.Accepted || writes != 0 {
		t.Fatal("receipt lookup submitted a correction")
	}
	status, result = post("/ui/messages/edit", "forged", values)
	if status != 403 || writes != 0 {
		t.Fatal("CSRF rejection reached the engine")
	}
	status, result = post("/ui/messages/edit", csrf, values)
	if status != 200 || !result.Accepted || result.Command == nil || result.Command.OriginalText != values.Get("originalText") || writes != 1 || !strings.Contains(result.Message, "conexão") {
		t.Fatalf("correction acceptance missing: %d %+v writes%d", status, result, writes)
	}
	status, result = post("/ui/messages/edit", csrf, values)
	if status != 200 || !result.Accepted || writes != 1 {
		t.Fatal("matching receipt replay submitted twice")
	}
	changed := url.Values{}
	for k, v := range values {
		changed[k] = append([]string(nil), v...)
	}
	changed.Set("originalText", "Another body")
	status, result = post("/ui/messages/edit", csrf, changed)
	if status != 409 || result.Accepted || result.Rejected || writes != 1 {
		t.Fatal("frozen receipt mismatch changed command or claimed rejection")
	}
	foreign := url.Values{}
	for k, v := range values {
		foreign[k] = append([]string(nil), v...)
	}
	foreign.Set("accountId", testChat)
	status, result = post("/ui/messages/edit-receipt", csrf, foreign)
	if status != 409 || result.Command != nil {
		t.Fatal("foreign account exposed a command")
	}
	conflict = true
	changed.Set("operationKey", newOperationKey())
	status, result = post("/ui/messages/edit", csrf, changed)
	if status != 409 || !result.Rejected || result.Accepted || writes != 2 {
		t.Fatal("definitive changed-head rejection lost")
	}
	if receiptReads < 5 {
		t.Fatal("correction did not check receipts before submission")
	}
}
