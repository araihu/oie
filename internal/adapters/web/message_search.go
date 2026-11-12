package web

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/goshtoso/components/combobox"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed message-search.js
var messageSearchScript []byte

//go:embed message-search.css
var messageSearchStylesheet []byte

const searchConversationsOnly = "conversations"
const searchMessagesOnly = "messages"

var searchResultLinks = regexp.MustCompile(`https?://[^\s<>"\x00-\x1f]+`)

func searchResultText(hit engineapi.MessageSearchHit) (excerpt, sharedURL string) {
	if hit.Historical || hit.MatchKind != "metadata" {
		return hit.Excerpt, ""
	}
	body := strings.TrimSpace(messageView{Message: hit.Message}.displayBody())
	const limit = 512
	for _, candidate := range searchResultLinks.FindAllString(body, 32) {
		candidate = strings.TrimRight(candidate, ".,;!)]}")
		if sharedURL == "" {
			sharedURL = candidate
		}
		if strings.Contains(hit.Excerpt, candidate) {
			sharedURL = candidate
			break
		}
	}
	if sharedURL != "" {
		body = strings.TrimSpace(strings.Replace(body, sharedURL, "", 1))
	}
	return shortenSearchExcerpt(body, limit), shortenSearchExcerpt(sharedURL, limit)
}

func shortenSearchExcerpt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func messageSearchStylesheetURL() string {
	digest := sha256.Sum256(messageSearchStylesheet)
	return fmt.Sprintf("/ui/message-search.css?v=%x", digest[:8])
}

type messageSearchView struct {
	Contacts                                                                     []engineapi.Contact
	ContactsKnown                                                                bool
	ContactsPartial                                                              bool
	ContactsLimited                                                              bool
	Conversations                                                                []engineapi.Conversation
	ConversationsLimited                                                         bool
	Category                                                                     string
	Query, AccountID, ConversationID, Cursor, RequestedEngine, ConversationTitle string
	AccountIDs                                                                   []string
	IncludeHistory                                                               bool
	Limit                                                                        int
	State                                                                        string
	Status                                                                       int
	Page                                                                         *engineapi.MessageSearchPage
}

func (s messageSearchView) accounts() []string {
	if s.AccountID != "" {
		return []string{s.AccountID}
	}
	return s.AccountIDs
}

func (s messageSearchView) includesAccount(id string) bool {
	return (s.AccountID == "" || id == s.AccountID) && (len(s.AccountIDs) == 0 || slices.Contains(s.AccountIDs, id))
}

func searchAccountSelection(ids []string) (string, []string) {
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", ids
}

func (v *view) configureMessageSearch(r *http.Request) {
	q := r.URL.Query()
	native := r.URL.Path == "/search" || r.URL.Path == "/ui/message-search"
	v.MessageSearch = messageSearchView{Limit: 30, State: "blank"}
	s := &v.MessageSearch
	s.Category = q.Get("searchType")
	if native {
		s.Query = q.Get("q")
		s.AccountID = q.Get("accountId")
		s.AccountIDs = q["accountIds"]
		if r.URL.Path == "/search" && (q.Has("nativeAccountFilter") || q.Has("nativeAccountId")) {
			s.AccountID, s.AccountIDs = searchAccountSelection(q["nativeAccountId"])
		}
		s.ConversationID = q.Get("conversationId")
		s.Cursor = q.Get("cursor")
		s.RequestedEngine = q.Get("engineId")
	} else {
		s.Query = q.Get("searchQuery")
		s.AccountID, s.AccountIDs = searchAccountSelection(q["searchAccount"])
		s.ConversationID = q.Get("searchConversation")
		s.Cursor = q.Get("searchCursor")
		s.RequestedEngine = q.Get("searchEngine")
	}
	s.Query = strings.TrimSpace(s.Query)
	if s.Category != "" && s.Category != searchMessagesOnly && s.Category != searchConversationsOnly ||
		s.Category == searchConversationsOnly && (s.ConversationID != "" || s.Cursor != "") {
		s.State = "invalid"
		s.Status = 400
	}
	slices.Sort(s.AccountIDs)
	if len(s.AccountIDs) > 100 || s.AccountID != "" && len(s.AccountIDs) > 0 {
		s.State = "invalid"
		s.Status = 400
	}
	for i, id := range s.AccountIDs {
		if !validOperationKey(id) || i > 0 && id == s.AccountIDs[i-1] {
			s.State = "invalid"
			s.Status = 400
		}
	}
	history := q.Get("includeEditHistory")
	s.IncludeHistory = history == "true"
	if history != "" && history != "true" && history != "false" {
		s.State = "invalid"
		s.Status = 400
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 100 {
			s.State = "invalid"
			s.Status = 400
		} else {
			s.Limit = n
		}
	}
	if r.URL.Path == "/ui/message-search" && s.RequestedEngine == "" {
		s.State = "invalid"
		s.Status = 400
	}
	if !utf8.ValidString(s.Query) || len([]rune(s.Query)) > 200 || len(s.Cursor) > 2048 || !utf8.ValidString(s.Cursor) {
		s.State = "invalid"
		s.Status = 400
	}
	for _, id := range []string{s.AccountID, s.ConversationID, s.RequestedEngine} {
		if id != "" && !validOperationKey(id) {
			s.State = "invalid"
			s.Status = 400
		}
	}
}
func (s *Server) loadMessageSearch(ctx context.Context, v *view) {
	state := &v.MessageSearch
	if state.State == "invalid" {
		return
	}
	if state.RequestedEngine != "" && state.RequestedEngine != v.EngineID {
		state.State = "engine_changed"
		state.Status = 409
		return
	}
	if state.ConversationID != "" {
		owner, err := s.client.GetConversationWithResponse(ctx, state.ConversationID)
		if err != nil {
			state.State = "unavailable"
			state.Status = 503
			return
		}
		if owner.StatusCode() != 404 && owner.JSON200 == nil {
			state.State = "unavailable"
			state.Status = 503
			return
		}
		if owner.JSON200 == nil || owner.JSON200.Id != state.ConversationID || !state.includesAccount(owner.JSON200.AccountId) {
			state.State = "missing"
			state.Status = 404
			return
		}
		state.ConversationTitle = owner.JSON200.Title
	}
	if state.Query == "" {
		state.State = "blank"
		return
	}
	state.State = "unavailable"
	state.Status = 503
	if state.Category == searchConversationsOnly {
		if s.loadSearchConversations(ctx, v) {
			state.State, state.Status = "ready", 200
			s.loadSearchConversationAvatars(ctx, v)
		} else {
			state.Conversations = nil
			state.ConversationsLimited = false
		}
		return
	}
	params := engineapi.SearchMessagesParams{Q: state.Query, IncludeEditHistory: &state.IncludeHistory, Limit: &state.Limit}
	if state.AccountID != "" {
		params.AccountId = &state.AccountID
	}
	if len(state.AccountIDs) > 0 {
		params.AccountIds = &state.AccountIDs
	}
	if state.ConversationID != "" {
		params.ConversationId = &state.ConversationID
	}
	if state.Cursor != "" {
		params.Cursor = &state.Cursor
	}
	response, err := s.client.SearchMessagesWithResponse(ctx, &params)
	if err != nil {
		return
	}
	if response.StatusCode() == 400 {
		state.State = "invalid"
		state.Status = 400
		return
	}
	if response.StatusCode() == 404 {
		state.State = "missing"
		state.Status = 404
		return
	}
	if response.JSON200 == nil || !validMessageSearchPage(*response.JSON200, *state) {
		return
	}
	state.Page = response.JSON200
	state.State = "ready"
	state.Status = 200
	if state.Category == "" && state.ConversationID == "" && state.Cursor == "" {
		s.loadSearchConversations(ctx, v)
		s.loadSearchContacts(ctx, v)
		s.loadSearchConversationAvatars(ctx, v)
	}
}

func (s *Server) loadSearchConversationAvatars(ctx context.Context, v *view) {
	if !v.NativeMessageSearch {
		return
	}
	accounts := map[string]bool{}
	for _, conversation := range v.MessageSearch.Conversations {
		if !accounts[conversation.AccountId] {
			s.loadAvatars(ctx, v, conversation.AccountId)
			accounts[conversation.AccountId] = true
		}
	}
}
func validMessageSearchPage(page engineapi.MessageSearchPage, request messageSearchView) bool {
	value := func(p *string) string {
		if p != nil {
			return *p
		}
		return ""
	}
	var accountIDs []string
	if page.AccountIds != nil {
		accountIDs = *page.AccountIds
	}
	if page.Query != request.Query || value(page.AccountId) != request.AccountID || !slices.Equal(accountIDs, request.AccountIDs) || value(page.ConversationId) != request.ConversationID || page.IncludeEditHistory != request.IncludeHistory || len(page.Items) > request.Limit {
		return false
	}
	if page.NextCursor != nil && (*page.NextCursor == "" || *page.NextCursor == request.Cursor || len(*page.NextCursor) > 2048) {
		return false
	}
	if page.MetadataIndexing.State != "pending" && page.MetadataIndexing.State != "complete" || page.MetadataIndexing.IndexedMessages < 0 || page.MetadataIndexing.TotalMessages < page.MetadataIndexing.IndexedMessages {
		return false
	}
	seen := map[string]bool{}
	for i, hit := range page.Items {
		m := hit.Message
		if !validOperationKey(m.Id) || !validOperationKey(m.RevisionId) || !validOperationKey(m.AccountId) || !validOperationKey(m.ConversationId) || !validOperationKey(hit.MatchedRevisionId) || m.CreatedAt.IsZero() || hit.MatchedAt.IsZero() || len([]rune(hit.Excerpt)) > 512 {
			return false
		}
		if !request.includesAccount(m.AccountId) || request.ConversationID != "" && m.ConversationId != request.ConversationID {
			return false
		}
		if hit.MatchKind != "text" && hit.MatchKind != "metadata" || hit.Historical && !request.IncludeHistory {
			return false
		}
		key := m.Id + ":" + hit.MatchedRevisionId
		if seen[key] {
			return false
		}
		seen[key] = true
		if i > 0 {
			previous := page.Items[i-1]
			if previous.Message.CreatedAt.Truncate(time.Millisecond).Before(m.CreatedAt.Truncate(time.Millisecond)) || previous.Message.CreatedAt.Truncate(time.Millisecond).Equal(m.CreatedAt.Truncate(time.Millisecond)) && previous.Message.Id < m.Id || previous.Message.Id == m.Id && previous.Historical && !hit.Historical {
				return false
			}
		}
	}
	return true
}
func (s *Server) messageSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	v := s.baseView(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	engine, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || engine.JSON200 == nil {
		v.MessageSearch.State = "unavailable"
		v.MessageSearch.Status = 503
	} else {
		v.EngineID = engine.JSON200.Id
		s.loadMessageSearch(ctx, &v)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if v.MessageSearch.Status >= 400 {
		w.WriteHeader(v.MessageSearch.Status)
	}
	_ = messageSearchResponse(v).Render(presentationContext(r.Context(), v), w)
}
func (v view) messageSearchAccountCombobox() combobox.Config {
	options := make([]combobox.Option, 0, len(v.Accounts))
	for _, account := range v.Accounts {
		options = append(options, combobox.Option{Value: account.Id, Label: account.Label + " · " + account.Address})
	}
	return combobox.Config{
		ID: "message-search-account", Name: "accountId", Label: v.Text.Accounts, Placeholder: v.Text.AllAccounts,
		Mode: combobox.ModeMultiple, Source: combobox.Source{Static: options}, Selected: v.MessageSearch.accounts(),
		EnableSearch: true, EnableClearAll: true, DisablePersistence: true,
	}
}

func (v view) messageSearchAccountCount(count int) string {
	if count == 0 {
		return v.Text.AllAccounts
	}
	label := v.Text.SearchAccountsSelected
	if count == 1 {
		label = v.Text.SearchAccountSelected
	}
	return strings.ReplaceAll(label, "{count}", strconv.Itoa(count))
}
func (v view) messageSearchHref(account, conversation string) string {
	q := url.Values{"lang": {v.Locale}, "theme": {v.Theme}}
	if account != "" {
		q.Set("accountId", account)
	}
	if conversation != "" {
		q.Set("conversationId", conversation)
	}
	return "/search?" + q.Encode()
}
func (v view) messageSearchNativeHref(cursor string) string {
	q := url.Values{"q": {v.MessageSearch.Query}, "lang": {v.Locale}, "theme": {v.Theme}}
	if v.MessageSearch.Category != "" {
		q.Set("searchType", v.MessageSearch.Category)
	}
	if v.EngineID != "" {
		q.Set("engineId", v.EngineID)
	}
	if v.MessageSearch.AccountID != "" {
		q.Set("accountId", v.MessageSearch.AccountID)
	}
	if len(v.MessageSearch.AccountIDs) > 0 {
		q["accountIds"] = v.MessageSearch.AccountIDs
	}
	if v.MessageSearch.ConversationID != "" {
		q.Set("conversationId", v.MessageSearch.ConversationID)
	}
	if v.MessageSearch.IncludeHistory {
		q.Set("includeEditHistory", "true")
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return "/search?" + q.Encode()
}
func (v view) messageSearchSourceHref(m engineapi.Message) string { return v.infoSourceHref(m) }

func searchNameParts(label string) (name, domain string) {
	label = strings.TrimSpace(label)
	if validProfileAddress(label) {
		name, domain, _ = strings.Cut(label, "@")
		return name, domain
	}
	return label, ""
}

func searchIdentityParts(label, address string) (name, detail string) {
	if strings.TrimSpace(label) == "" {
		return searchNameParts(address)
	}
	name, detail = searchNameParts(label)
	if detail == "" && name != address {
		detail = address
	}
	return name, detail
}

func (v view) messageSearchNext() string {
	if v.MessageSearch.Page != nil && v.MessageSearch.Page.NextCursor != nil {
		return *v.MessageSearch.Page.NextCursor
	}
	return ""
}
func (v view) blankMessageSearchView() view {
	v.MessageSearch.Query = ""
	v.MessageSearch.Cursor = ""
	v.MessageSearch.State = "blank"
	v.MessageSearch.Page = nil
	v.MessageSearch.Contacts = nil
	v.MessageSearch.ContactsKnown = false
	v.MessageSearch.ContactsPartial = false
	v.MessageSearch.ContactsLimited = false
	v.MessageSearch.Conversations = nil
	v.MessageSearch.ConversationsLimited = false
	return v
}
func (v view) messageSearchAllHref() string {
	out := v
	out.MessageSearch.ConversationID = ""
	out.MessageSearch.Cursor = ""
	return out.messageSearchNativeHref("")
}

func (s *Server) loadSearchConversations(ctx context.Context, v *view) bool {
	state := &v.MessageSearch
	limit := 20
	seen := map[string]bool{}
	complete := true
	for _, archived := range []bool{false, true} {
		params := engineapi.ListConversationsParams{Q: &state.Query, Limit: &limit, Archived: &archived}
		if state.AccountID != "" {
			params.AccountId = &state.AccountID
		}
		if len(state.AccountIDs) > 0 {
			params.AccountIds = &state.AccountIDs
		}
		response, err := s.client.ListConversationsWithResponse(ctx, &params)
		if err != nil || response.JSON200 == nil {
			complete = false
			continue
		}
		state.ConversationsLimited = state.ConversationsLimited || response.JSON200.NextCursor != nil || len(response.JSON200.Items) >= limit
		for _, c := range response.JSON200.Items {
			if !validOperationKey(c.Id) || !validOperationKey(c.AccountId) || !state.includesAccount(c.AccountId) || seen[c.Id] {
				continue
			}
			seen[c.Id] = true
			state.Conversations = append(state.Conversations, c)
		}
	}
	sort.Slice(state.Conversations, func(i, j int) bool {
		a, b := state.Conversations[i], state.Conversations[j]
		if a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.Id > b.Id
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
	if len(state.Conversations) > limit {
		state.Conversations = state.Conversations[:limit]
		state.ConversationsLimited = true
	}
	return complete
}
func (v view) messageSearchConversationHref(c engineapi.Conversation) string {
	return v.href("section", "chats", "chat", c.Id, "account", c.AccountId, "message", "", "q", "", "filter", "all")
}

func (s *Server) loadSearchContacts(ctx context.Context, v *view) {
	state := &v.MessageSearch
	limit := 20
	params := engineapi.ListContactsParams{Q: &state.Query, Limit: &limit}
	if state.AccountID != "" {
		params.AccountId = &state.AccountID
	}
	if len(state.AccountIDs) > 0 {
		params.AccountIds = &state.AccountIDs
	}
	response, err := s.client.ListContactsWithResponse(ctx, &params)
	if err != nil || response.JSON200 == nil || len(response.JSON200.Items) > limit {
		return
	}
	page := response.JSON200
	sources := map[string]bool{}
	selected := state.accounts()
	allKnown := len(page.Sources) > 0 && (len(selected) == 0 || len(page.Sources) == len(selected))
	for _, source := range page.Sources {
		if !validOperationKey(source.AccountId) || !state.includesAccount(source.AccountId) || sources[source.AccountId] || source.State != "known" && source.State != "unknown" && source.State != "unavailable" {
			return
		}
		sources[source.AccountId] = true
		allKnown = allKnown && source.State == "known"
	}
	seen := map[string]bool{}
	for _, c := range page.Items {
		if !validOperationKey(c.Id) || !validOperationKey(c.RevisionId) || !sources[c.AccountId] || seen[c.Id] || c.Address == "" || c.DisplayName == "" || c.ConversationId != nil && !validOperationKey(*c.ConversationId) {
			return
		}
		seen[c.Id] = true
	}
	state.Contacts = page.Items
	state.ContactsKnown = allKnown
	state.ContactsPartial = !allKnown
	state.ContactsLimited = page.NextCursor != nil
}
func (v view) messageSearchContactHref(c engineapi.Contact) string {
	if c.ConversationId != nil {
		return v.href("section", "chats", "chat", *c.ConversationId, "account", c.AccountId, "message", "", "q", "", "filter", "all")
	}
	return v.href("section", "chats", "account", c.AccountId, "chat", "", "open", "newchat", "newChatAddress", c.Address)
}

func (v view) messageSearchCategoryHref(kind string) string {
	return v.href("section", "media", "contentType", kind, "contentCursor", "", "chat", "", "message", "", "q", v.MessageSearch.Query, "account", v.MessageSearch.AccountID)
}

func (v view) messageSearchFilterHref(category string) string {
	if v.NativeMessageSearch {
		v.MessageSearch.Category = category
		return v.messageSearchNativeHref("")
	}
	return v.href("searchType", category)
}
