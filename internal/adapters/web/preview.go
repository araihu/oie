package web

import (
	_ "embed"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed preview-landscape.svg
var previewLandscape []byte

func (v *view) configureWorkspace(r *http.Request) {
	q := r.URL.Query()
	v.ChatID = q.Get("chat")
	v.InboxOnly = q.Get("inbox") == "1"
	v.ChatOpen = v.ChatID != "" && !v.InboxOnly
	v.FocusMessage = q.Get("message")
	v.TimelineCursor = q.Get("timelineCursor")
	v.TimelineDirection = q.Get("timelineDirection")
	v.AccountFilter = q.Get("account")
	v.AccountPage = q.Get("accountPage")
	v.ManagedAccount = q.Get("managedAccount")
	v.Section = q.Get("section")
	switch v.Section {
	case "favorites", "archived", "media", "scheduled":
	default:
		v.Section = "chats"
	}
	if v.Section == "media" && q.Has("contentAccount") {
		// A present but empty selection overrides the global account filter.
		v.SharedAccountIDs = slices.DeleteFunc(slices.Clone(q["contentAccount"]), func(id string) bool { return id == "" })
		slices.Sort(v.SharedAccountIDs)
	}
	v.Filter = "all"
	if v.Section != "archived" {
		switch filter := q.Get("filter"); filter {
		case "unread", "favorites", "groups":
			v.Filter = filter
		}
	}
	v.InfoView = q.Get("infoView")
	if v.InfoView != "shared" && v.InfoView != "stars" && v.InfoView != "participants" {
		v.InfoView = ""
	}
	v.InfoType = q.Get("infoType")
	if v.InfoType != "links" && v.InfoType != "documents" {
		v.InfoType = "media"
	}
	v.InfoCursor = q.Get("infoCursor")
	v.InfoQuery = q.Get("infoQuery")
	if len([]rune(v.InfoQuery)) > 200 {
		v.InfoQuery = string([]rune(v.InfoQuery)[:200])
	}
	v.InfoFocus = q.Get("infoFocus")
	v.SharedType = q.Get("contentType")
	switch v.SharedType {
	case "links", "documents":
	default:
		v.SharedType = "media"
	}
	if v.Section == "favorites" {
		v.FavoritesCursor = q.Get("favoritesCursor")
		query := []rune(strings.TrimSpace(q.Get("favoritesQuery")))
		v.FavoritesQuery = string(query[:min(len(query), 200)])
	}
	v.ScheduleCursor = q.Get("scheduleCursor")
	v.ScheduleConversations = slices.DeleteFunc(q["scheduleConversation"], func(id string) bool { return id == "" })
	slices.Sort(v.ScheduleConversations)
	v.ScheduleConversations = slices.Compact(v.ScheduleConversations)
	v.SharedCursor = q.Get("contentCursor")
	v.SharedConversation = q.Get("contentConversation")
	v.Query = q.Get("q")
	if query := []rune(v.Query); len(query) > 200 {
		v.Query = string(query[:200])
	}
}
func (v view) href(pairs ...string) string {
	q := url.Values{}
	sharedAccounts := v.SharedAccountIDs
	if v.AccountFilter != "" {
		q.Set("account", v.AccountFilter)
	}
	if v.Section != "chats" {
		q.Set("section", v.Section)
	}
	if v.Filter != "all" {
		q.Set("filter", v.Filter)
	}
	if v.Query != "" {
		q.Set("q", v.Query)
	}
	if v.InboxOnly {
		q.Set("inbox", "1")
	}
	if (v.ChatOpen || v.InboxOnly) && v.ChatID != "" {
		q.Set("chat", v.ChatID)
		if v.FocusMessage != "" {
			q.Set("message", v.FocusMessage)
		}
	}
	if v.InfoView != "" {
		q.Set("infoView", v.InfoView)
		q.Set("infoType", v.InfoType)
	}
	if v.InfoCursor != "" {
		q.Set("infoCursor", v.InfoCursor)
	}
	if v.InfoQuery != "" {
		q.Set("infoQuery", v.InfoQuery)
	}
	if v.Section == "media" {
		q.Set("contentType", v.SharedType)
		if v.SharedCursor != "" {
			q.Set("contentCursor", v.SharedCursor)
		}
		if v.SharedConversation != "" {
			q.Set("contentConversation", v.SharedConversation)
		}
	}
	if v.Section == "favorites" {
		if v.FavoritesCursor != "" {
			q.Set("favoritesCursor", v.FavoritesCursor)
		}
		if v.FavoritesQuery != "" {
			q.Set("favoritesQuery", v.FavoritesQuery)
		}
	}
	if v.Section == "scheduled" {
		if len(v.ScheduleConversations) > 0 {
			q["scheduleConversation"] = v.ScheduleConversations
		}
		if v.ScheduleCursor != "" {
			q.Set("scheduleCursor", v.ScheduleCursor)
		}
	}
	if v.GlobalMessageSearch && !v.NativeMessageSearch {
		q.Set("searchMode", "messages")
		q.Set("searchQuery", v.MessageSearch.Query)
		q["searchAccount"] = v.MessageSearch.accounts()
		if v.MessageSearch.Category != "" {
			q.Set("searchType", v.MessageSearch.Category)
		}
		q.Set("searchEngine", v.EngineID)
		if v.MessageSearch.IncludeHistory {
			q.Set("includeEditHistory", "true")
		}
	}
	if v.ConversationSearchOpen {
		q.Set("chatSearch", "1")
		q.Set("chatQuery", v.ConversationSearchQuery)
		if v.ConversationSearchHistory {
			q.Set("chatIncludeHistory", "true")
		}
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] == "section" && (pairs[i+1] == "media" || pairs[i+1] == "scheduled" || pairs[i+1] == "favorites") {
			for _, key := range []string{"searchMode", "searchQuery", "searchAccount", "searchType", "searchEngine", "includeEditHistory", "chatSearch", "chatQuery", "chatIncludeHistory"} {
				q.Del(key)
			}
		}
		if pairs[i] == "section" && pairs[i+1] != "media" {
			q.Del("contentType")
			q.Del("contentCursor")
			q.Del("contentConversation")
		}
		if pairs[i] == "section" && pairs[i+1] != "scheduled" {
			q.Del("scheduleConversation")
			q.Del("scheduleCursor")
		}
		if pairs[i] == "section" && pairs[i+1] != "favorites" {
			q.Del("favoritesQuery")
		}
		if pairs[i] == "section" && pairs[i+1] != "favorites" || pairs[i] == "account" && pairs[i+1] != v.AccountFilter || pairs[i] == "favoritesQuery" && pairs[i+1] != v.FavoritesQuery {
			q.Del("favoritesCursor")
		}
		if pairs[i] == "account" {
			if pairs[i+1] != v.AccountFilter || sharedAccounts != nil {
				q.Del("contentCursor")
			}
			sharedAccounts = nil
		}
		if pairs[i] == "chat" && pairs[i+1] != v.ChatID {
			q.Del("chatSearch")
			q.Del("chatQuery")
			q.Del("chatIncludeHistory")
			for _, key := range []string{"infoView", "infoType", "infoCursor", "infoQuery", "infoFocus"} {
				q.Del(key)
			}
			q.Del("message")
		}
		if pairs[i] == "chat" {
			q.Del("inbox")
		}
		if pairs[i+1] == "" {
			q.Del(pairs[i])
		} else {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	if q.Get("section") == "media" {
		if len(sharedAccounts) > 0 {
			q["contentAccount"] = sharedAccounts
		} else if sharedAccounts != nil && q.Get("account") != "" {
			q.Set("contentAccount", "")
		}
	}
	if len(q) == 0 {
		return "/"
	}
	return "/?" + q.Encode()
}

func (v view) filterHref(filter string) string {
	inbox := ""
	if !v.ChatOpen {
		inbox = "1"
	}
	return v.href("filter", filter, "chat", v.ChatID, "inbox", inbox, "message", v.FocusMessage,
		"timelineCursor", v.TimelineCursor, "timelineDirection", v.TimelineDirection, "lang", v.Locale, "theme", v.Theme)
}
func initials(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "?"
	}
	out := []rune(parts[0])[:1]
	if len(parts) > 1 {
		out = append(out, []rune(parts[len(parts)-1])[0])
	}
	return strings.ToUpper(string(out))
}

func chatInitials(c engineapi.Conversation) string { return initials(c.Title) }

func (v view) accountScreen() string {
	if v.FormTarget == "register" {
		return "add"
	}
	if strings.HasPrefix(v.FormTarget, "connect-") || strings.HasPrefix(v.FormTarget, "disconnect-") {
		return "detail"
	}
	if v.AccountPage == "capabilities" {
		return "capabilities"
	}
	if v.AccountPage == "add" {
		return "add"
	}
	if v.ManagedAccount != "" {
		return "detail"
	}
	return "list"
}
func (v view) managedAccountID() string {
	if strings.HasPrefix(v.FormTarget, "connect-") {
		return strings.TrimPrefix(v.FormTarget, "connect-")
	}
	if strings.HasPrefix(v.FormTarget, "disconnect-") {
		return strings.TrimPrefix(v.FormTarget, "disconnect-")
	}
	return v.ManagedAccount
}
func (v view) accountsHref(id string) string {
	return v.href("open", "accounts", "accountPage", "", "managedAccount", id)
}
func (v view) addAccountHref() string {
	return v.href("open", "accounts", "accountPage", "add", "managedAccount", "")
}
func (v view) initialDrawer() string {
	if v.FormTarget == "join-room" || v.FormTarget == "create-room" {
		return v.FormTarget
	}
	if v.FormTarget == "register" || strings.HasPrefix(v.FormTarget, "connect-") || strings.HasPrefix(v.FormTarget, "disconnect-") {
		return "accounts"
	}
	return ""
}
