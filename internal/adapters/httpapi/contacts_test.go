package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
)

func TestContactHTTPQueriesAreScopedCachedAndReadOnlyAfterRestart(t *testing.T) {
	dir := t.TempDir()
	protocol := &protocolStub{}
	e, store, server := newMessagingEngine(t, dir, protocol)
	personal := openTestConversation(t, e, "contact-personal@localhost")
	work := openTestConversation(t, e, "contact-work@localhost")
	items := []application.ContactInput{}
	for i := range 106 {
		name := "Unrelated"
		if i < 5 {
			name = "Café <script>literal</script>"
		}
		address := fmt.Sprintf("buddy%03d@localhost", i)
		if i == 0 {
			address = personal.Address
		}
		items = append(items, application.ContactInput{Address: address, DisplayName: name, Subscription: "both", Groups: []string{"Team"}})
	}
	if err := e.ContactsReceived(t.Context(), personal.AccountID, items); err != nil {
		t.Fatal(err)
	}
	if err := e.ContactsReceived(t.Context(), work.AccountID, []application.ContactInput{{Address: "foreign@localhost", DisplayName: "Café work", Subscription: "none"}}); err != nil {
		t.Fatal(err)
	}
	before, err := e.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{"q": {"cafe"}, "accountId": {string(personal.AccountID)}, "limit": {"2"}}
	read := func(path string) engineapi.ContactPage {
		t.Helper()
		status, body := request(t, server.URL, "GET", path, "", "")
		if status != 200 {
			t.Fatalf("contacts %d %s", status, body)
		}
		var page engineapi.ContactPage
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		if page.Items == nil || page.Sources == nil {
			t.Fatal("null result arrays")
		}
		return page
	}
	first := read("/api/v1/contacts?" + query.Encode())
	if len(first.Items) != 2 || first.NextCursor == nil || len(first.Sources) != 1 || first.Sources[0].State != "known" {
		t.Fatal(first)
	}
	var seen []string
	page := first
	for {
		for _, item := range page.Items {
			if item.AccountId != string(personal.AccountID) {
				t.Fatal("foreign contact leaked")
			}
			seen = append(seen, item.Id)
		}
		if page.NextCursor == nil {
			break
		}
		query.Set("cursor", *page.NextCursor)
		page = read("/api/v1/contacts?" + query.Encode())
	}
	if len(seen) != 5 {
		t.Fatal("contact filtering happened after limit", len(seen))
	}
	foreignCursor := url.Values{"q": {"cafe"}, "accountId": {string(work.AccountID)}, "cursor": {*first.NextCursor}}
	for _, path := range []string{"/api/v1/contacts?" + foreignCursor.Encode(), "/api/v1/contacts?q=%00", "/api/v1/contacts?limit=101", "/api/v1/contacts?accountId=invalid", "/api/v1/contacts?cursor=invalid"} {
		if code, _ := request(t, server.URL, "GET", path, "", ""); code != 400 {
			t.Fatal("invalid contact query", code, path)
		}
	}
	if code, _ := request(t, server.URL, "GET", "/api/v1/contacts?accountId="+string(id()), "", ""); code != 404 {
		t.Fatal("unknown account", code)
	}
	if code, _ := request(t, server.URL, http.MethodPost, "/api/v1/contacts", "", ""); code != 405 {
		t.Fatal("query accepted mutation", code)
	}
	after, _ := e.Snapshot(t.Context())
	if before != after || protocol.count != 0 {
		t.Fatal("directory query changed message state or dispatched", before, after, protocol.count)
	}
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	e, _, server = newMessagingEngine(t, dir, protocol)
	query.Del("cursor")
	restored := read("/api/v1/contacts?" + query.Encode())
	if !reflect.DeepEqual(first, restored) {
		t.Fatal("directory identity/revisions changed after restart")
	}
	if err = e.ContactsUnavailable(t.Context(), personal.AccountID); err != nil {
		t.Fatal(err)
	}
	cached := read("/api/v1/contacts?" + query.Encode())
	if len(cached.Items) != 2 || cached.Sources[0].State != "unavailable" || cached.Sources[0].CheckedAt == nil {
		t.Fatal("failed refresh destroyed cached contacts", cached)
	}
}
