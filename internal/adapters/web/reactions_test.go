package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestReactionBrowserReceiptScopesAndImmutableSet(t *testing.T) {
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	commands := map[string]engineapi.MessageReactionCommand{}
	writes := 0
	reads := 0
	snapshot := engineapi.MessageReactions{MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, OwnEmojis: []string{}, Items: []engineapi.ReactionItem{}, Eligible: true, MaxEmojis: 8}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/engine":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": testEngine, "role": "hosted", "apiVersion": "0.26.0", "eventCursor": testEngine + ":0"})
		case "/api/v1/messages/" + testMessage:
			_ = json.NewEncoder(w).Encode(engineapi.Message{Id: testMessage, AccountId: testAccount, ConversationId: testChat, RevisionId: testRevision, CreatedAt: at})
		case "/api/v1/messages/" + testMessage + "/reactions":
			if r.Method == "GET" {
				reads++
				_ = json.NewEncoder(w).Encode(snapshot)
				return
			}
			writes++
			var body engineapi.SetMessageReactions
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			command := engineapi.MessageReactionCommand{Id: r.Header.Get("Idempotency-Key"), RevisionId: testRevision, MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, ExpectedOwnRevisionId: body.ExpectedOwnRevisionId, Emojis: body.Emojis, State: "unknown", CreatedAt: at, UpdatedAt: at}
			commands[command.Id] = command
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(command)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/v1/message-reaction-commands/") {
				command, ok := commands[strings.TrimPrefix(r.URL.Path, "/api/v1/message-reaction-commands/")]
				if !ok {
					w.WriteHeader(404)
					_ = json.NewEncoder(w).Encode(map[string]string{"code": "not_found"})
					return
				}
				_ = json.NewEncoder(w).Encode(command)
				return
			}
			t.Errorf("unexpectedrequest %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer backend.Close()
	ui, err := New(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "messageId": {testMessage}, "lang": {"en"}}
	get := httptest.NewRequest("GET", "/ui/message-reactions?"+query.Encode(), nil)
	out := httptest.NewRecorder()
	ui.ServeHTTP(out, get)
	var preparation reactionPreparation
	if err := json.Unmarshal(out.Body.Bytes(), &preparation); err != nil {
		t.Fatal(err)
	}
	if out.Code != 200 || reads != 1 || writes != 0 || !validOperationKey(preparation.OperationKey) {
		t.Fatalf("read-onlypreparationfailed %d %+v", out.Code, preparation)
	}
	seed := httptest.NewRecorder()
	token := csrfToken(seed, httptest.NewRequest("GET", "/", nil))
	form := url.Values{"csrf": {token}, "engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "messageId": {testMessage}, "operationKey": {preparation.OperationKey}, "emojis": {"[\"👍\",\"😂\"]"}, "expectedOwnRevisionId": {""}, "lang": {"en"}}
	post := func(path string, values url.Values) (int, reactionResult) {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(seed.Result().Cookies()[0])
		w := httptest.NewRecorder()
		ui.ServeHTTP(w, r)
		var result reactionResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return w.Code, result
	}
	code, result := post("/ui/messages/reaction-receipt", form)
	if code != 404 || result.Accepted || writes != 0 {
		t.Fatal("receiptlookupcreatedreaction")
	}
	code, result = post("/ui/messages/reactions", form)
	if code != 200 || !result.Accepted || result.Command == nil || result.Command.State != "unknown" || writes != 1 {
		t.Fatalf("acceptancefailed %d %+v", code, result)
	}
	form.Set("emojis", "[\"😂\",\"👍\"]")
	code, result = post("/ui/messages/reactions", form)
	if code != 200 || !result.Accepted || writes != 1 {
		t.Fatal("setorderreplaycreatedreaction")
	}
	form.Set("emojis", "[]")
	code, result = post("/ui/messages/reaction-receipt", form)
	if code != 409 || result.Command != nil || result.Rejected || writes != 1 {
		t.Fatal("frozenpayloadmismatchleakedcommand")
	}
	form.Set("accountId", testChat)
	code, result = post("/ui/messages/reaction-receipt", form)
	if code != 404 || result.Command != nil || writes != 1 {
		t.Fatal("foreignaccountleakedcommand")
	}
	form.Set("accountId", testAccount)
	form.Set("csrf", "forged")
	code, _ = post("/ui/messages/reactions", form)
	if code != 403 || writes != 1 {
		t.Fatal("CSRFrejectionreachedmutation")
	}
}

func TestReactionAllowlistIsIndependentOfSelectionLimit(t *testing.T) {
	choices := engineapi.ReactionEmojiSet{}
	for n := 0; n < 32; n++ {
		choices = append(choices, string(rune(0x1f600+n)))
	}
	snapshot := engineapi.MessageReactions{MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, OwnEmojis: []string{}, Items: []engineapi.ReactionItem{}, Eligible: true, MaxEmojis: 1, AllowedEmojis: &choices}
	if !validReactions(snapshot, testAccount, testChat, testMessage) {
		t.Fatal("legitimateallowlistincorrectlylimitedtoselectionlimit")
	}
	snapshot.Items = []engineapi.ReactionItem{{Emoji: "👍", Count: 1, Own: true}}
	if validReactions(snapshot, testAccount, testChat, testMessage) {
		t.Fatal("unverifiedownchipaccepted")
	}
}

func TestReactionActorSnapshotValidation(t *testing.T) {
	actors := []engineapi.ReactionActor{{Address: "alice@localhost", DisplayName: "Alice", Own: true, Emojis: []string{"👍"}}, {Address: "bob@localhost", DisplayName: "Bob <img>", Emojis: []string{"👍"}}}
	valid := engineapi.MessageReactions{MessageId: testMessage, AccountId: testAccount, ConversationId: testChat, OwnEmojis: []string{"👍"}, Items: []engineapi.ReactionItem{{Emoji: "👍", Count: 2, Own: true}}, Actors: &actors, Eligible: true, MaxEmojis: 8}
	if !validReactions(valid, testAccount, testChat, testMessage) {
		t.Fatal("valid verified actor roster rejected")
	}
	for name, mutate := range map[string]func(*engineapi.MessageReactions){
		"duplicate actor": func(s *engineapi.MessageReactions) { (*s.Actors)[1].Address = (*s.Actors)[0].Address },
		"foreign avatar URL": func(s *engineapi.MessageReactions) {
			url := "https://untrusted.example/avatar"
			(*s.Actors)[1].AvatarHash = &url
		},
		"count mismatch": func(s *engineapi.MessageReactions) { s.Items[0].Count = 3 },
		"forged own":     func(s *engineapi.MessageReactions) { (*s.Actors)[1].Own = true },
		"empty set":      func(s *engineapi.MessageReactions) { (*s.Actors)[1].Emojis = []string{} },
		"control name":   func(s *engineapi.MessageReactions) { (*s.Actors)[1].DisplayName = "Bob\x00" },
	} {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(valid)
			var snapshot engineapi.MessageReactions
			if err := json.Unmarshal(data, &snapshot); err != nil {
				t.Fatal(err)
			}
			mutate(&snapshot)
			if validReactions(snapshot, testAccount, testChat, testMessage) {
				t.Fatal("untrusted actor projection accepted")
			}
		})
	}
	valid.Actors = nil
	if !validReactions(valid, testAccount, testChat, testMessage) {
		t.Fatal("older engine snapshot lost compatibility")
	}
}
