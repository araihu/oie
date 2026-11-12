package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type searchHTTPHit struct {
	Message                         engineapi.Message
	AccountLabel, ConversationTitle string
	MatchedRevisionID               string `json:"matchedRevisionId"`
	MatchedAt                       time.Time
	Historical                      bool
	MatchKind, Excerpt              string
}
type searchHTTPPage struct {
	Query, AccountID, ConversationID string
	AccountIDs                       []string
	IncludeEditHistory               bool
	Items                            []searchHTTPHit
	NextCursor                       string
	MetadataIndexing                 struct {
		State                          string
		IndexedMessages, TotalMessages int
	}
}

func TestSearchAccountSelectionAppliesBeforePagination(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	conversations := []domain.Conversation{
		openTestConversation(t, e, "personal@localhost"),
		openTestConversation(t, e, "work@localhost"),
		openTestConversation(t, e, "other@localhost"),
	}
	for i, c := range conversations {
		at := time.Now().Add(time.Duration(i) * time.Minute).UTC()
		m := domain.Message{ID: id(), RevisionID: id(), AccountID: c.AccountID, ConversationID: c.ID, Sender: domain.Sender{ID: c.PeerID, DisplayName: "Peer"}, OriginalText: "Search scope marker", CreatedAt: at, SendState: "received"}
		if err := store.ReceiveMessage(t.Context(), c, m, id()); err != nil {
			t.Fatal(err)
		}
		if err := e.ContactsReceived(t.Context(), c.AccountID, []application.ContactInput{{Address: "bob@localhost", DisplayName: "Search scope marker", Subscription: "both"}}); err != nil {
			t.Fatal(err)
		}
	}
	personal, work, other := string(conversations[0].AccountID), string(conversations[1].AccountID), string(conversations[2].AccountID)
	selected := []string{personal, work}
	slices.Sort(selected)
	query := url.Values{"q": {"search scope marker"}, "accountIds": {selected[1], selected[0]}, "limit": {"1"}}
	first := searchHTTP(t, server.URL, query)
	if !slices.Equal(first.AccountIDs, selected) || first.AccountID != "" || len(first.Items) != 1 || first.Items[0].Message.AccountId != work || first.NextCursor == "" || first.MetadataIndexing.TotalMessages != 2 {
		t.Fatal("selected accounts must be applied before both paging and coverage", first)
	}
	query["accountIds"] = selected
	query.Set("cursor", first.NextCursor)
	second := searchHTTP(t, server.URL, query)
	if len(second.Items) != 1 || second.Items[0].Message.AccountId != personal || second.NextCursor != "" {
		t.Fatal("reordering the selected accounts changed the result scope", second)
	}
	query["accountIds"] = []string{work, other}
	status, _ := request(t, server.URL, http.MethodGet, "/api/v1/messages/search?"+query.Encode(), "", "")
	if status != http.StatusBadRequest {
		t.Fatalf("a different account selection accepted the cursor: %d", status)
	}

	query = url.Values{"q": {"search scope marker"}, "accountIds": selected, "limit": {"1"}}
	status, body := request(t, server.URL, http.MethodGet, "/api/v1/conversations?"+query.Encode(), "", "")
	var titles engineapi.ConversationPage
	if status != http.StatusOK || json.Unmarshal(body, &titles) != nil || len(titles.Items) != 1 || titles.Items[0].AccountId == other || titles.NextCursor == nil {
		t.Fatalf("conversation matches were not scoped before pagination: %d %s", status, body)
	}
	status, body = request(t, server.URL, http.MethodGet, "/api/v1/contacts?"+query.Encode(), "", "")
	var contacts engineapi.ContactPage
	if status != http.StatusOK || json.Unmarshal(body, &contacts) != nil || len(contacts.Items) != 1 || contacts.Items[0].AccountId == other || len(contacts.Sources) != 2 || contacts.NextCursor == nil {
		t.Fatalf("contact matches or coverage were not scoped: %d %s", status, body)
	}
	for _, source := range contacts.Sources {
		if !slices.Contains(selected, source.AccountId) {
			t.Fatal("contact coverage included an unselected account", source)
		}
	}
}

func searchHTTP(t *testing.T, server string, parameters url.Values) searchHTTPPage {
	t.Helper()
	status, body := request(t, server, http.MethodGet, "/api/v1/messages/search?"+parameters.Encode(), "", "")
	if status != http.StatusOK {
		t.Fatalf("search: %d %s", status, body)
	}
	var page searchHTTPPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if page.Items == nil {
		t.Fatal("search must return an explicit items array")
	}
	for _, hit := range page.Items {
		if hit.Message.Id == "" || hit.MatchedRevisionID == "" || hit.MatchedAt.IsZero() || hit.AccountLabel == "" || hit.ConversationTitle == "" || utf8.RuneCountInString(hit.Excerpt) > 512 {
			t.Fatal("incomplete source-linked search hit", hit)
		}
	}
	return page
}

func TestMessageSearchHTTPScopesFiltersBeforePagingAndPreservesState(t *testing.T) {
	protocol := &protocolStub{}
	e, store, server := newMessagingEngine(t, t.TempDir(), protocol)
	c := openTestConversation(t, e, "search-personal@localhost")
	foreign := openTestConversation(t, e, "search-work@localhost")
	second, err := e.OpenConversation(t.Context(), id(), c.AccountID, "carol@localhost")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	seed := func(conversation domain.Conversation, n int, text string, created time.Time) domain.Message {
		t.Helper()
		message := domain.Message{ID: domain.ID(fmt.Sprintf("019a5df0-3333-7000-8000-%012d", n)), RevisionID: id(), AccountID: conversation.AccountID, ConversationID: conversation.ID, Sender: domain.Sender{ID: conversation.PeerID, DisplayName: "Peer"}, OriginalText: text, CreatedAt: created, Historical: true, SendState: "received", ProviderID: fmt.Sprintf("search-%d", n)}
		if err := store.ReceiveMessage(t.Context(), conversation, message, id()); err != nil {
			t.Fatal(err)
		}
		return message
	}
	// Matching sources are older than more than one page of nonmatches. A LIMIT
	// before the text predicate would incorrectly return no matches here.
	var wanted []domain.Message
	for n := 1; n <= 9; n++ {
		wanted = append(wanted, seed(c, n, fmt.Sprintf("Telescope result %d", n), at.Add(time.Duration(n)*time.Second)))
	}
	for n := 0; n < 110; n++ {
		seed(c, 100+n, "Unrelated newer retained content", at.Add(time.Hour+time.Duration(n)*time.Second))
	}
	otherChat := seed(second.Conversation, 300, "Telescope same account other conversation", at.Add(2*time.Hour))
	otherAccount := seed(foreign, 301, "Telescope other account", at.Add(3*time.Hour))
	unicode := seed(c, 302, "Café decomposed cafe\u0301 😀 <script>literal</script> & text", at.Add(4*time.Hour))
	duplicate := seed(c, 303, wanted[0].OriginalText, wanted[0].CreatedAt)
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: "A live unread source", ProviderID: "unread-search-source"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SendMessage(t.Context(), id(), c.ID, "Preserve the queued delivery body"); err != nil {
		t.Fatal(err)
	}
	c, _ = e.Conversation(t.Context(), c.ID)
	yes := true
	if _, err := e.UpdateConversationPreferences(t.Context(), id(), c.ID, application.ConversationPreferencesInput{AccountID: c.AccountID, ExpectedRevisionID: c.RevisionID, Archived: &yes}); err != nil {
		t.Fatal(err)
	}
	before, _ := e.Snapshot(t.Context())
	conversationBefore, _ := e.Conversation(t.Context(), c.ID)
	readBefore, _ := e.ConversationReadState(t.Context(), c.ID)
	historyBefore, _ := e.History(t.Context(), c.AccountID)
	pendingBefore, _ := store.PendingMessages(t.Context(), time.Now().Add(time.Hour))
	parameters := url.Values{"q": {"  telescope  "}, "conversationId": {string(c.ID)}, "limit": {"2"}}
	var found []string
	seen := map[string]bool{}
	for n := 0; n < 10; n++ {
		page := searchHTTP(t, server.URL, parameters)
		if page.Query != "telescope" || page.ConversationID != string(c.ID) || page.IncludeEditHistory {
			t.Fatal("search context was not canonical", page)
		}
		for _, hit := range page.Items {
			if hit.Message.ConversationId != string(c.ID) || hit.Historical || hit.MatchKind != "text" || seen[hit.Message.Id] {
				t.Fatal("scope leak or repeated canonical row", hit)
			}
			seen[hit.Message.Id] = true
			found = append(found, hit.Message.Id)
		}
		if page.NextCursor == "" {
			break
		}
		if len(page.Items) != 2 || page.NextCursor == parameters.Get("cursor") {
			t.Fatal("bounded search cursor did not advance", page)
		}
		parameters.Set("cursor", page.NextCursor)
	}
	var expected []string
	for n := len(wanted) - 1; n >= 1; n-- {
		expected = append(expected, string(wanted[n].ID))
	}
	expected = append(expected, string(duplicate.ID), string(wanted[0].ID))
	if !reflect.DeepEqual(found, expected) {
		t.Fatal("search skipped old matches, collapsed same text or changed timestamp/UUID order", found, expected)
	}
	global := searchHTTP(t, server.URL, url.Values{"q": {"telescope"}})
	if len(global.Items) != 12 || global.Items[0].Message.Id != string(otherAccount.ID) || global.Items[1].Message.Id != string(otherChat.ID) {
		t.Fatal("global search must include each account and archived source", global)
	}
	accountPage := searchHTTP(t, server.URL, url.Values{"q": {"telescope"}, "accountId": {string(c.AccountID)}})
	if len(accountPage.Items) != 11 || accountPage.AccountID != string(c.AccountID) {
		t.Fatal("account search scope", accountPage)
	}
	for _, hit := range accountPage.Items {
		if hit.Message.AccountId != string(c.AccountID) {
			t.Fatal("account search returned a foreign source")
		}
	}
	accent := searchHTTP(t, server.URL, url.Values{"q": {"CAFÉ 😀"}, "conversationId": {string(c.ID)}})
	if len(accent.Items) != 1 || accent.Items[0].Message.Id != string(unicode.ID) || accent.Items[0].Message.OriginalText != unicode.OriginalText {
		t.Fatal("Unicode matching must retain exact source text", accent)
	}
	first := searchHTTP(t, server.URL, url.Values{"q": {"telescope"}, "limit": {"1"}})
	for name, query := range map[string]url.Values{
		"query": {"q": {"unrelated"}}, "account": {"q": {"telescope"}, "accountId": {string(c.AccountID)}},
		"conversation": {"q": {"telescope"}, "conversationId": {string(c.ID)}}, "history": {"q": {"telescope"}, "includeEditHistory": {"true"}},
	} {
		query.Set("cursor", first.NextCursor)
		status, data := request(t, server.URL, http.MethodGet, "/api/v1/messages/search?"+query.Encode(), "", "")
		if status != 400 {
			t.Fatalf("%s accepted another search's cursor: %d %s", name, status, data)
		}
	}
	for _, query := range []url.Values{{}, {"q": {" \t "}}, {"q": {"\x00"}}, {"q": {strings.Repeat("é", 201)}}, {"q": {string([]byte{0xff})}}, {"q": {"telescope"}, "limit": {"0"}}, {"q": {"telescope"}, "limit": {"101"}}, {"q": {"telescope"}, "cursor": {"malformed"}}, {"q": {"telescope"}, "accountId": {"invalid"}}, {"q": {"telescope"}, "conversationId": {"invalid"}}, {"q": {"telescope"}, "includeEditHistory": {"invalid"}}} {
		status, data := request(t, server.URL, http.MethodGet, "/api/v1/messages/search?"+query.Encode(), "", "")
		if status != 400 {
			t.Fatalf("invalid query accepted: %d %s", status, data)
		}
	}
	for _, query := range []string{`%_`, `" OR 1=1 --`, `NEAR(*, telescope)`, `<script>missingvalue</script>`} {
		page := searchHTTP(t, server.URL, url.Values{"q": {query}})
		if len(page.Items) != 0 {
			t.Fatalf("query syntax became an operator: %q %+v", query, page.Items)
		}
	}
	if after, _ := e.Snapshot(t.Context()); after.Sequence != before.Sequence {
		t.Fatal("search published events")
	}
	if after, _ := e.Conversation(t.Context(), c.ID); !reflect.DeepEqual(after, conversationBefore) {
		t.Fatal("search changed conversation placement, activity or unread state")
	}
	if after, _ := e.ConversationReadState(t.Context(), c.ID); !reflect.DeepEqual(after, readBefore) {
		t.Fatal("search marked messages read")
	}
	if after, _ := e.History(t.Context(), c.AccountID); !reflect.DeepEqual(after, historyBefore) {
		t.Fatal("search fetched or changed provider history")
	}
	if after, _ := store.PendingMessages(t.Context(), time.Now().Add(time.Hour)); !reflect.DeepEqual(after, pendingBefore) || len(after) != 1 {
		t.Fatal("search changed queued delivery work")
	}
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	if protocol.count != 0 {
		t.Fatal("search called the protocol adapter")
	}
}

func TestMessageSearchHTTPMetadataCoverageExcludesAnObsoleteEditedCatalog(t *testing.T) {
	e, store, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "search-metadata@localhost")
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	const address = "https://files.example.test/asset.pdf"
	original := application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "metadata-original", OriginalText: "Read this attachment " + address, CreatedAt: at, Historical: true, Correction: &domain.MessageCorrection{Scope: "xmpp:sender:bob@localhost", Author: "bob@localhost", ID: "metadata-original"}}
	if err := e.Receive(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	sources, _ := e.Messages(t.Context(), c.ID, "", 50)
	source := sources[0]
	if err := store.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: source, ParserVersion: application.SharedContentVersion, References: []domain.SharedReference{{ID: id(), RevisionID: id(), Kind: "document", URL: address, FileName: "Constellation blueprint.pdf"}}}}); err != nil {
		t.Fatal(err)
	}
	indexed := searchHTTP(t, server.URL, url.Values{"q": {"constellation"}, "conversationId": {string(c.ID)}})
	if len(indexed.Items) != 1 || indexed.Items[0].Message.Id != string(source.ID) || indexed.Items[0].MatchKind != "metadata" || indexed.Items[0].Historical || indexed.MetadataIndexing.State != "complete" || indexed.MetadataIndexing.IndexedMessages != 1 || indexed.MetadataIndexing.TotalMessages != 1 {
		t.Fatal("indexed metadata must retain current source and truthful coverage", indexed)
	}
	correction := original
	correction.ProviderID, correction.OriginalText, correction.CreatedAt = "metadata-correction", "A changed source without an attachment", at.Add(time.Minute)
	correction.Correction = &domain.MessageCorrection{Scope: original.Correction.Scope, Author: original.Correction.Author, ID: correction.ProviderID, TargetID: original.ProviderID}
	if err := e.Receive(t.Context(), correction); err != nil {
		t.Fatal(err)
	}
	for _, include := range []string{"false", "true"} {
		stale := searchHTTP(t, server.URL, url.Values{"q": {"constellation"}, "includeEditHistory": {include}, "conversationId": {string(c.ID)}})
		if len(stale.Items) != 0 || stale.MetadataIndexing.State != "pending" || stale.MetadataIndexing.IndexedMessages != 0 || stale.MetadataIndexing.TotalMessages != 1 {
			t.Fatal("stale metadata must not resurrect an obsolete attachment", stale)
		}
	}
	current, _ := e.Message(t.Context(), source.ID)
	if err := store.IndexSharedContent(t.Context(), []domain.SharedContentMutation{{Message: current, ParserVersion: application.SharedContentVersion}}); err != nil {
		t.Fatal(err)
	}
	rebuilt := searchHTTP(t, server.URL, url.Values{"q": {"changed"}, "conversationId": {string(c.ID)}})
	if len(rebuilt.Items) != 1 || rebuilt.Items[0].MatchKind != "text" || rebuilt.Items[0].Message.OriginalText != correction.OriginalText || rebuilt.MetadataIndexing.State != "complete" {
		t.Fatal("refreshed catalog coverage must use the canonical edited source", rebuilt)
	}
}

func TestMessageSearchHTTPCurrentAndHistoricalCorrectionHitsStaySourceLinked(t *testing.T) {
	dir := t.TempDir()
	e, store, server := newMessagingEngine(t, dir, &protocolStub{})
	c := openTestConversation(t, e, "search-revisions@localhost")
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	receive := func(provider, body, target string, created time.Time) {
		t.Helper()
		if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, OriginalText: body, ProviderID: provider, CreatedAt: created, Historical: true, Correction: &domain.MessageCorrection{Scope: "xmpp:sender:bob@localhost", Author: "bob@localhost", ID: provider, TargetID: target}}); err != nil {
			t.Fatal(err)
		}
	}
	receive("original-search", "Pineapple original version", "", at)
	originals, _ := e.Messages(t.Context(), c.ID, "", 50)
	canonicalID, originalRevision := originals[0].ID, originals[0].RevisionID
	receive("first-edit-search", "Pineapple earlier correction", "original-search", at.Add(500*time.Millisecond))
	receive("latest-edit-search", "Current telescope café <script>literal</script>", "first-edit-search", at.Add(time.Second))
	current, _ := e.Message(t.Context(), canonicalID)
	if page := searchHTTP(t, server.URL, url.Values{"q": {"pineapple"}}); len(page.Items) != 0 {
		t.Fatal("default search revived a superseded body", page)
	}
	history := searchHTTP(t, server.URL, url.Values{"q": {"pineapple"}, "includeEditHistory": {"true"}, "limit": {"1"}})
	var historical []searchHTTPHit
	for n := 0; n < 3; n++ {
		historical = append(historical, history.Items...)
		if history.NextCursor == "" {
			break
		}
		history = searchHTTP(t, server.URL, url.Values{"q": {"pineapple"}, "includeEditHistory": {"true"}, "cursor": {history.NextCursor}, "limit": {"1"}})
	}
	if len(historical) != 2 || historical[0].MatchedRevisionID == historical[1].MatchedRevisionID || historical[1].MatchedRevisionID != string(originalRevision) {
		t.Fatal("history must contain distinct retained content versions, not transport revisions", historical)
	}
	for _, hit := range historical {
		if !hit.Historical || hit.Message.Id != string(canonicalID) || hit.Message.OriginalText != current.OriginalText || hit.Message.RevisionId != string(current.RevisionID) || !hit.Message.CreatedAt.Equal(at) || !strings.Contains(strings.ToLower(hit.Excerpt), "pineapple") {
			t.Fatal("historical excerpt was confused with the current source or creation time", hit)
		}
		status, data := request(t, server.URL, http.MethodGet, "/api/v1/conversations/"+string(c.ID)+"/messages/"+hit.Message.Id+"/context?limit=1", "", "")
		var window engineapi.MessageContext
		if err := json.Unmarshal(data, &window); err != nil || status != 200 || window.TargetId != string(canonicalID) || len(window.Items) != 1 || window.Items[0].OriginalText != current.OriginalText {
			t.Fatal("search source jump must use bounded canonical current context", status, string(data), err)
		}
	}
	page := searchHTTP(t, server.URL, url.Values{"q": {"telescope"}, "includeEditHistory": {"true"}})
	if len(page.Items) != 1 || page.Items[0].Historical || page.Items[0].MatchedRevisionID != string(current.RevisionID) {
		t.Fatal("current content must not duplicate a transport revision", page)
	}
	queued, err := e.SendMessage(t.Context(), id(), c.ID, "Sole delivery state content")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.CancelMessage(t.Context(), id(), queued.Message.ID, queued.Message.RevisionID); err != nil {
		t.Fatal(err)
	}
	delivery := searchHTTP(t, server.URL, url.Values{"q": {"Sole delivery state"}, "includeEditHistory": {"true"}})
	if len(delivery.Items) != 1 || delivery.Items[0].Historical || delivery.Items[0].Message.Id != string(queued.Message.ID) || delivery.Items[0].Message.SendState != "cancelled" {
		t.Fatal("delivery transitions must not invent edit-history matches", delivery)
	}
	server.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, restarted := newMessagingEngine(t, dir, &protocolStub{})
	restored := searchHTTP(t, restarted.URL, url.Values{"q": {"pineapple"}, "includeEditHistory": {"true"}})
	if !reflect.DeepEqual(restored.Items, historical) {
		t.Fatal("search content revisions must survive restart", restored.Items, historical)
	}
}
