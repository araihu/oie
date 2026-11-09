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

func preferenceRequest(t *testing.T, s *Server, values url.Values, native bool) *httptest.ResponseRecorder {
	t.Helper()
	seed := httptest.NewRecorder()
	token := csrfToken(seed, httptest.NewRequest("GET", "/", nil))
	values.Set("csrf", token)
	r := httptest.NewRequest("POST", "/ui/conversations/preferences", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(seed.Result().Cookies()[0])
	if !native {
		r.Header.Set("Accept", "application/json")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func preferenceValues() url.Values {
	return url.Values{"engineId": {testEngine}, "accountId": {testAccount}, "conversationId": {testChat}, "operationKey": {testMessage}, "expectedRevisionId": {testRevision}, "archived": {"true"}, "returnTo": {"/?chat=" + testChat + "&lang=en"}, "formTarget": {preferenceFormID(testChat, "archived")}, "lang": {"en"}}
}
func preferenceConversation() engineapi.Conversation {
	return engineapi.Conversation{Id: testChat, AccountId: testAccount, RevisionId: testRevision, Kind: "direct", Title: "Bob", Address: "bob@localhost", UpdatedAt: time.Now().UTC()}
}

func TestConversationPreferenceBFFUsesCanonicalReadNotReceiptSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		write, read, want       int
		drop, pending, rejected bool
	}{
		{"historical replay", 200, 200, 200, false, false, false},
		{"stale revision", 409, 200, 409, false, false, true},
		{"legacy unsupported method", 405, 200, 405, false, false, true},
		{"lost committed reply", 200, 200, 503, true, true, false},
		{"accepted but current unavailable", 200, 503, 503, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			reads := 0
			canonical := preferenceConversation()
			canonical.Favorite = true
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/engine" {
					_ = json.NewEncoder(w).Encode(map[string]string{"id": testEngine})
					return
				}
				if r.URL.Path != "/api/v1/conversations/"+testChat {
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(404)
					return
				}
				calls = append(calls, r.Method)
				if r.Method == "PATCH" {
					if r.Header.Get("Idempotency-Key") != testMessage {
						t.Error("operation key changed")
					}
					var input engineapi.UpdateConversationPreferences
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.AccountId != testAccount || input.ExpectedRevisionId != testRevision || input.Archived == nil || !*input.Archived || input.Favorite != nil {
						t.Errorf("frozen command changed: %+v %v", input, err)
					}
					if tc.drop {
						conn, _, _ := w.(http.Hijacker).Hijack()
						_ = conn.Close()
						return
					}
					w.WriteHeader(tc.write)
					if tc.write == 405 {
						return
					}
					if tc.write == 409 {
						_ = json.NewEncoder(w).Encode(engineapi.Problem{Status: 409, Code: "state_conflict", CurrentConversation: &canonical})
						return
					}
					historical := canonical
					historical.Archived = true
					historical.Favorite = false
					w.Header().Set("Idempotency-Replayed", "true")
					_ = json.NewEncoder(w).Encode(historical)
					return
				}
				reads++
				if reads > 1 {
					w.WriteHeader(tc.read)
					if tc.read != 200 {
						return
					}
				}
				_ = json.NewEncoder(w).Encode(canonical)
			}))
			defer api.Close()
			ui, err := New(api.URL)
			if err != nil {
				t.Fatal(err)
			}
			out := preferenceRequest(t, ui, preferenceValues(), false)
			var result mutationResult
			if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if out.Code != tc.want || result.PreferencesPending != tc.pending || result.Rejected != tc.rejected {
				t.Fatalf("unexpected outcome %d %s", out.Code, out.Body.String())
			}
			if len(calls) < 3 || calls[0] != "GET" || calls[len(calls)-1] != "GET" {
				t.Fatalf("canonical read missing: %v", calls)
			}
			for _, method := range calls[1 : len(calls)-1] {
				if method != "PATCH" {
					t.Fatalf("unexpected request between scoped canonical reads: %v", calls)
				}
			}
			if tc.read == 200 && (result.Conversation == nil || result.Conversation.Archived || !result.Conversation.Favorite) {
				t.Fatal("historical receipt was projected instead of current state", out.Body.String())
			}
			if tc.pending && result.OK {
				t.Fatal("uncertain intent reported success")
			}
		})
	}
}
func TestConversationPreferenceBFFRejectsWrongScopeAndAmbiguousIntentBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(url.Values)
		want   int
	}{
		{"wrong account", func(v url.Values) { v.Set("accountId", testMessage) }, 400},
		{"two fields", func(v url.Values) { v.Set("favorite", "true") }, 422},
		{"duplicate desired", func(v url.Values) { v.Add("archived", "false") }, 422},
		{"implicit toggle", func(v url.Values) { v.Set("archived", "toggle") }, 422},
		{"duplicate owner", func(v url.Values) { v.Add("accountId", testAccount) }, 422},
		{"missing expected revision", func(v url.Values) { v.Del("expectedRevisionId") }, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			base := &liveFixture{}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					w.WriteHeader(500)
					return
				}
				base.ServeHTTP(w, r)
			}))
			defer api.Close()
			ui, _ := New(api.URL)
			values := preferenceValues()
			tc.change(values)
			out := preferenceRequest(t, ui, values, false)
			if out.Code != tc.want || writes != 0 {
				t.Fatalf("guard failed %d %s writes=%d", out.Code, out.Body.String(), writes)
			}
		})
	}
}
func TestConversationPreferenceNativePRGAndFrozenRecovery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "uncertain"}[fail], func(t *testing.T) {
			base := &liveFixture{}
			canonical := preferenceConversation()
			canonical.Archived = true
			canonical.RevisionId = testEngine
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/conversations/"+testChat {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == "PATCH" && fail {
						w.WriteHeader(503)
						return
					}
					_ = json.NewEncoder(w).Encode(canonical)
					return
				}
				base.ServeHTTP(w, r)
			}))
			defer api.Close()
			ui, _ := New(api.URL)
			values := preferenceValues()
			values.Set("returnTo", "/?lang=en")
			out := preferenceRequest(t, ui, values, true)
			if !fail {
				if out.Code != 303 || out.Header().Get("Location") != "/?chat="+testChat+"&lang=en" {
					t.Fatalf("PRG lost route %d %s", out.Code, out.Header().Get("Location"))
				}
				return
			}
			html := out.Body.String()
			if out.Code != 503 || !strings.Contains(html, ui.catalogs["en"].RetryConversationPreference) {
				t.Fatalf("native uncertain recovery missing %d %s", out.Code, html)
			}
			start := strings.Index(html, `class="oie-native-recovery"`)
			if start < 0 {
				t.Fatal("no recovery form")
			}
			recovery := html[start:]
			recovery = recovery[:strings.Index(recovery, "</form>")]
			for _, want := range []string{`name="operationKey" value="` + testMessage + `"`, `name="expectedRevisionId" value="` + testRevision + `"`, `name="archived" value="true"`} {
				if !strings.Contains(recovery, want) {
					t.Error("native retry changed frozen intent", want)
				}
			}
		})
	}
}

func TestConversationPreferenceCSRFMethodAndEngineGuards(t *testing.T) {
	calls, writes := 0, 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": testEngine})
	}))
	defer backend.Close()
	ui, _ := New(backend.URL)
	for _, method := range []string{"GET", "POST"} {
		request := httptest.NewRequest(method, "/ui/conversations/preferences", nil)
		request.Header.Set("Accept", "application/json")
		out := httptest.NewRecorder()
		ui.ServeHTTP(out, request)
		want := 405
		if method == "POST" {
			want = 403
		}
		if out.Code != want || calls != 0 {
			t.Fatalf("unguarded %s status=%d calls=%d", method, out.Code, calls)
		}
	}
	values := preferenceValues()
	values.Set("engineId", testRevision)
	out := preferenceRequest(t, ui, values, false)
	var result mutationResult
	_ = json.Unmarshal(out.Body.Bytes(), &result)
	if out.Code != 409 || !result.Rejected || result.Code != "engine_changed" || writes != 0 {
		t.Fatalf("wrong engine accepted: %d %s effects=%d", out.Code, out.Body.String(), writes)
	}
}
