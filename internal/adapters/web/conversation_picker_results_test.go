package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestPickerPaginatesMatchingChatsButLimitsRecents(t *testing.T) {
	for _, tc := range []struct {
		name, term, cursor, limit string
		wantMore                  bool
	}{
		{"recent", "", "", "5", false},
		{"search", "a", "", "24", true},
		{"next", "a", "opaque+next", "24", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != "/api/v1/conversations" || q.Get("accountId") != testAccount ||
					q.Get("q") != tc.term || q.Get("limit") != tc.limit || q.Get("cursor") != tc.cursor {
					t.Errorf("unexpected conversation query %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(engineapi.ConversationPage{NextCursor: new("following-page")})
			}))
			defer backend.Close()
			ui, err := New(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			params := url.Values{"group": {"chats"}, "account": {testAccount}, "term": {tc.term}, "cursor": {tc.cursor}}
			req := httptest.NewRequest(http.MethodGet, "/ui/new-conversation/results?"+params.Encode(), nil)
			res := httptest.NewRecorder()
			ui.ServeHTTP(res, req)
			if res.Code != http.StatusOK || strings.Contains(res.Body.String(), `data-picker-more="following-page"`) != tc.wantMore {
				t.Fatalf("unexpected result %d %s", res.Code, res.Body)
			}
		})
	}
}

func TestPickerIndicatesIncompleteRosterCoverage(t *testing.T) {
	text := copy{Unavailable: "unavailable", SearchContactsPartial: "partial"}
	for _, tc := range []struct {
		name   string
		states []engineapi.ContactSourceState
		cached bool
		want   string
	}{
		{"no accounts", nil, false, ""},
		{"complete empty", []engineapi.ContactSourceState{"known"}, false, ""},
		{"never synced", []engineapi.ContactSourceState{"unknown"}, false, "unavailable"},
		{"failed with cached rows", []engineapi.ContactSourceState{"unavailable"}, true, "partial"},
		{"mixed", []engineapi.ContactSourceState{"known", "unknown"}, false, "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var page engineapi.ContactPage
			for _, state := range tc.states {
				page.Sources = append(page.Sources, engineapi.ContactSource{State: state})
			}
			if tc.cached {
				page.Items = []engineapi.Contact{{}}
			}
			if got := pickerContactCoverage(text, page); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
