package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func preferenceRequest(t *testing.T, server string, conversation domain.Conversation, key domain.ID, flags string, replayed ...bool) (int, []byte) {
	t.Helper()
	body := fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q,%s}`, conversation.AccountID, conversation.RevisionID, flags)
	if len(replayed) == 0 {
		return request(t, server, http.MethodPatch, "/api/v1/conversations/"+string(conversation.ID), string(key), body)
	}
	req, err := http.NewRequest(http.MethodPatch, server+"/api/v1/conversations/"+string(conversation.ID), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", string(key))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode == 200 && response.Header.Get("Idempotency-Replayed") != fmt.Sprint(replayed[0]) {
		t.Fatal("accepted response must identify receipt replay", response.Header)
	}
	return response.StatusCode, data
}

func preferenceConversation(t *testing.T, data []byte) engineapi.Conversation {
	t.Helper()
	var conversation engineapi.Conversation
	if err := json.Unmarshal(data, &conversation); err != nil {
		t.Fatal(err)
	}
	return conversation
}

func TestConversationPreferencesHTTPPreservesRetainedStateAndReplaysAfterRestart(t *testing.T) {
	dir, protocol := t.TempDir(), &protocolStub{}
	e, store, server := newMessagingEngine(t, dir, protocol)
	c := openTestConversation(t, e, "preferences@localhost")
	other := openTestConversation(t, e, "independent@localhost")
	for n, historical := range []bool{false, false, true} {
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: fmt.Sprintf("Retained %d", n), ProviderID: fmt.Sprintf("preferences-%d", n), Historical: historical}); err != nil {
			t.Fatal(err)
		}
	}
	beforeMessages, err := e.Messages(t.Context(), c.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.MarkMessagesRead(t.Context(), id(), c.ID, []domain.ID{beforeMessages[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.SendMessage(t.Context(), id(), c.ID, "Preserve this disconnected queued send"); err != nil {
		t.Fatal(err)
	}
	if err = e.HistoryChanged(t.Context(), domain.HistoryStatus{AccountID: c.AccountID, State: "complete", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err = e.CommitHistoryCheckpoint(t.Context(), c.AccountID, "account", "retained-provider-cursor"); err != nil {
		t.Fatal(err)
	}
	c, _ = store.Conversation(t.Context(), c.ID)
	status, favoriteBody := preferenceRequest(t, server.URL, c, id(), `"favorite":true`)
	if status != 200 || !preferenceConversation(t, favoriteBody).Favorite {
		t.Fatalf("favorite acceptance: %d %s", status, favoriteBody)
	}
	before, _ := store.Conversation(t.Context(), c.ID)
	messages, _ := e.Messages(t.Context(), c.ID, "", 50)
	pending, _ := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	read, _ := e.ConversationReadState(t.Context(), c.ID)
	history, _ := e.History(t.Context(), c.AccountID)
	checkpoint, _ := e.HistoryCheckpoint(t.Context(), c.AccountID, "account")
	snapshot, _ := e.Snapshot(t.Context())
	key := id()
	status, accepted := preferenceRequest(t, server.URL, before, key, `"archived":true`, false)
	if status != 200 {
		t.Fatalf("archive acceptance: %d %s", status, accepted)
	}
	archived, err := store.Conversation(t.Context(), c.ID)
	if err != nil || !archived.Archived || !archived.Favorite || archived.RevisionID == before.RevisionID {
		t.Fatal("archive must preserve favorite and record a revision", archived, err)
	}
	expected := before
	expected.Archived, expected.RevisionID = true, archived.RevisionID
	if !reflect.DeepEqual(archived, expected) {
		t.Fatalf("preferences changed activity, timestamps, unread or identity: before=%+v after=%+v", before, archived)
	}
	if got, _ := e.Messages(t.Context(), c.ID, "", 50); !reflect.DeepEqual(got, messages) {
		t.Fatal("preference update changed retained messages")
	}
	if got, _ := store.PendingMessages(t.Context(), time.Now().Add(time.Hour)); !reflect.DeepEqual(got, pending) || len(got) != 1 {
		t.Fatal("preference update changed queued delivery work", got)
	}
	if got, _ := e.ConversationReadState(t.Context(), c.ID); !reflect.DeepEqual(got, read) {
		t.Fatal("preference update changed read state", got, read)
	}
	if got, _ := e.History(t.Context(), c.AccountID); !reflect.DeepEqual(got, history) {
		t.Fatal("preference update changed recovered history", got, history)
	}
	if got, _ := e.HistoryCheckpoint(t.Context(), c.AccountID, "account"); got != checkpoint {
		t.Fatal("preference update reset archive checkpoint", got)
	}
	if got, _ := store.Conversation(t.Context(), other.ID); !reflect.DeepEqual(got, other) {
		t.Fatal("preference update crossed account boundary")
	}
	events, err := e.Events(t.Context(), snapshot.Sequence)
	if err != nil || len(events) != 1 || events[0].Type != "conversation.updated" || events[0].Conversation == nil || !reflect.DeepEqual(*events[0].Conversation, archived) {
		t.Fatal("archive must emit one source-linked event", events, err)
	}
	sequence, _ := e.Snapshot(t.Context())
	status, noOp := preferenceRequest(t, server.URL, archived, id(), `"archived":true`, false)
	if status != 200 || !reflect.DeepEqual(preferenceConversation(t, noOp), preferenceConversation(t, accepted)) {
		t.Fatalf("already-current preference must succeed without a new revision: %d %s", status, noOp)
	}
	if after, _ := e.Snapshot(t.Context()); after.Sequence != sequence.Sequence {
		t.Fatal("no-op preference emitted another event")
	}
	if err = e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: "New arrival while archived", ProviderID: "preferences-arrival"}); err != nil {
		t.Fatal(err)
	}
	afterArrival, _ := store.Conversation(t.Context(), c.ID)
	if !afterArrival.Archived || !afterArrival.Favorite || afterArrival.UnreadCount != archived.UnreadCount+1 {
		t.Fatal("new arrival must retain archive/favorite and update unread", afterArrival)
	}
	status, removed := preferenceRequest(t, server.URL, afterArrival, id(), `"archived":false,"favorite":false`)
	if status != 200 || preferenceConversation(t, removed).Archived || preferenceConversation(t, removed).Favorite {
		t.Fatalf("unarchive and unfavorite: %d %s", status, removed)
	}
	current, _ := store.Conversation(t.Context(), c.ID)
	sequence, _ = e.Snapshot(t.Context())
	status, replay := preferenceRequest(t, server.URL, before, key, `"archived":true`, true)
	if status != 200 || !reflect.DeepEqual(preferenceConversation(t, replay), preferenceConversation(t, accepted)) {
		t.Fatalf("retry must return original accepted snapshot before current CAS: %d %s", status, replay)
	}
	if got, _ := store.Conversation(t.Context(), c.ID); !reflect.DeepEqual(got, current) {
		t.Fatal("old receipt revived an obsolete preference")
	}
	if after, _ := e.Snapshot(t.Context()); after.Sequence != sequence.Sequence {
		t.Fatal("receipt replay emitted an event")
	}
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, reopened, next := newMessagingEngine(t, dir, protocol)
	status, replay = preferenceRequest(t, next.URL, before, key, `"archived":true`, true)
	if status != 200 || !reflect.DeepEqual(preferenceConversation(t, replay), preferenceConversation(t, accepted)) {
		t.Fatalf("receipt must survive restart: %d %s", status, replay)
	}
	if got, _ := reopened.Conversation(t.Context(), c.ID); !reflect.DeepEqual(got, current) {
		t.Fatal("current preference snapshot must survive restart")
	}
	if operation, err := restarted.Operation(t.Context(), key); err != nil || operation.ResourceID != c.ID || operation.Kind != "update_conversation_preferences" {
		t.Fatal("durable operation must identify the exact conversation", operation, err)
	}
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	if protocol.count != 0 {
		t.Fatal("app preferences invoked protocol delivery", protocol.count)
	}
}

func TestConversationPreferencesHTTPRejectsInvalidOrStaleIntentsAtomically(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "preferences@localhost")
	foreign := openTestConversation(t, e, "foreign@localhost")
	key := id()
	status, body := preferenceRequest(t, server.URL, c, key, `"favorite":true`)
	if status != 200 {
		t.Fatalf("fixture preference: %d %s", status, body)
	}
	current, _ := store.Conversation(t.Context(), c.ID)
	sequence, _ := e.Snapshot(t.Context())
	valid := fmt.Sprintf(`"accountId":%q,"expectedRevisionId":%q`, c.AccountID, current.RevisionID)
	cases := []struct {
		name, path, key, body, code string
		status                      int
	}{
		{"missing key", string(c.ID), "", `{` + valid + `,"archived":true}`, "invalid_request", 400},
		{"invalid key", string(c.ID), "invalid", `{` + valid + `,"archived":true}`, "invalid_request", 400},
		{"missing flag", string(c.ID), string(id()), `{` + valid + `}`, "invalid_request", 400},
		{"null flag", string(c.ID), string(id()), `{` + valid + `,"archived":null}`, "invalid_request", 400},
		{"string flag", string(c.ID), string(id()), `{` + valid + `,"favorite":"true"}`, "invalid_request", 400},
		{"missing revision", string(c.ID), string(id()), fmt.Sprintf(`{"accountId":%q,"archived":true}`, c.AccountID), "invalid_request", 400},
		{"null revision", string(c.ID), string(id()), fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":null,"archived":true}`, c.AccountID), "invalid_request", 400},
		{"unknown field", string(c.ID), string(id()), `{` + valid + `,"archived":true,"extra":true}`, "invalid_request", 400},
		{"wrong account", string(c.ID), string(id()), fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q,"archived":true}`, foreign.AccountID, current.RevisionID), "invalid_request", 400},
		{"missing conversation", string(id()), string(id()), `{` + valid + `,"archived":true}`, "not_found", 404},
		{"stale revision", string(c.ID), string(id()), fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q,"archived":true}`, c.AccountID, c.RevisionID), "state_conflict", 409},
		{"stale no-op", string(c.ID), string(id()), fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q,"favorite":true}`, c.AccountID, c.RevisionID), "state_conflict", 409},
		{"changed idempotent body", string(c.ID), string(key), fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q,"favorite":false}`, c.AccountID, c.RevisionID), "idempotency_conflict", 409},
		{"changed precondition", string(c.ID), string(key), `{` + valid + `,"favorite":true}`, "idempotency_conflict", 409},
		{"changed field presence", string(c.ID), string(key), fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q,"favorite":true,"archived":false}`, c.AccountID, c.RevisionID), "idempotency_conflict", 409},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			status, data := request(t, server.URL, http.MethodPatch, "/api/v1/conversations/"+test.path, test.key, test.body)
			var problem engineapi.Problem
			if err := json.Unmarshal(data, &problem); err != nil || status != test.status || string(problem.Code) != test.code {
				t.Fatalf("unexpected rejection: %d %s %v", status, data, err)
			}
			if problem.Documentation == nil || !strings.HasSuffix(*problem.Documentation, "#"+test.code) {
				t.Fatalf("missing problem documentation: %s", data)
			}
			if test.code == "state_conflict" && (problem.CurrentConversation == nil || problem.CurrentConversation.RevisionId != string(current.RevisionID) || !problem.CurrentConversation.Favorite) {
				t.Fatal("stale request omitted canonical current preference", problem)
			}
			if test.name == "wrong account" && problem.CurrentConversation != nil {
				t.Fatal("wrong account must not receive a current snapshot")
			}
			if got, _ := store.Conversation(t.Context(), c.ID); !reflect.DeepEqual(got, current) {
				t.Fatal("rejected intent partially changed conversation", got)
			}
			if snapshot, _ := e.Snapshot(t.Context()); snapshot.Sequence != sequence.Sequence {
				t.Fatal("rejected intent published an event")
			}
			if test.key != string(key) && test.key != "" && test.key != "invalid" {
				if _, err := e.Operation(t.Context(), domain.ID(test.key)); err != application.ErrNotFound {
					t.Fatal("rejected intent stored a successful receipt", err)
				}
			}
		})
	}
}

func TestConversationPreferencesHTTPConcurrentCASHasOneWinner(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "preferences-race@localhost")
	before, _ := e.Snapshot(t.Context())
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(1)
	for _, flags := range []string{`"archived":true`, `"favorite":true`} {
		body := fmt.Sprintf(`{"accountId":%q,"expectedRevisionId":%q,%s}`, c.AccountID, c.RevisionID, flags)
		req, err := http.NewRequest(http.MethodPatch, server.URL+"/api/v1/conversations/"+string(c.ID), strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", string(id()))
		go func() {
			start.Wait()
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer func() { _ = response.Body.Close() }()
			data, err := io.ReadAll(response.Body)
			results <- result{status: response.StatusCode, body: data, err: err}
		}()
	}
	start.Done()
	one, two := <-results, <-results
	if one.err != nil || two.err != nil {
		t.Fatal(one.err, two.err)
	}
	if one.status != 200 {
		one, two = two, one
	}
	if one.status != 200 || two.status != 409 {
		t.Fatalf("concurrent intents must have one winner: %d %s / %d %s", one.status, one.body, two.status, two.body)
	}
	winner := preferenceConversation(t, one.body)
	var conflict engineapi.Problem
	if err := json.Unmarshal(two.body, &conflict); err != nil || conflict.Code != "state_conflict" || !reflect.DeepEqual(conflict.CurrentConversation, &winner) {
		t.Fatal("losing intent must expose winner's canonical snapshot", conflict, err)
	}
	current, _ := store.Conversation(t.Context(), c.ID)
	if current.Archived == current.Favorite || string(current.RevisionID) != winner.RevisionId {
		t.Fatal("concurrent fields were partially merged without a valid precondition", current)
	}
	if events, err := e.Events(t.Context(), before.Sequence); err != nil || len(events) != 1 || events[0].Type != "conversation.updated" {
		t.Fatal("concurrent CAS must publish exactly one accepted event", events, err)
	}
}
