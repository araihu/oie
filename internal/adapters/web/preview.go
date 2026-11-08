package web

import (
	_ "embed"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"net/http"
	"net/url"
	"strings"
)

//go:embed preview-landscape.svg
var previewLandscape []byte

func (v *view) configureWorkspace(r *http.Request) {
	q := r.URL.Query()
	v.ChatOpen = q.Get("chat") != ""
	v.ChatID = q.Get("chat")
	v.FocusMessage = q.Get("message")
	v.AccountFilter = q.Get("account")
	v.AccountPage = q.Get("accountPage")
	v.ManagedAccount = q.Get("managedAccount")
	v.Section = q.Get("section")
	switch v.Section {
	case "favorites", "archived", "media":
	default:
		v.Section = "chats"
	}
	v.Filter = q.Get("filter")
	switch v.Filter {
	case "unread", "favorites", "groups":
	default:
		v.Filter = "all"
	}
	v.SharedType = q.Get("contentType")
	switch v.SharedType {
	case "links", "documents":
	default:
		v.SharedType = "media"
	}
	v.SharedCursor = q.Get("contentCursor")
	v.SharedConversation = q.Get("contentConversation")
	v.Query = q.Get("q")
	if query := []rune(v.Query); len(query) > 200 {
		v.Query = string(query[:200])
	}
}
func (v view) href(pairs ...string) string {
	q := url.Values{}
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
	if v.ChatOpen && v.ChatID != "" {
		q.Set("chat", v.ChatID)
		if v.FocusMessage != "" {
			q.Set("message", v.FocusMessage)
		}
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
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i] == "section" && pairs[i+1] != "media" {
			q.Del("contentType")
			q.Del("contentCursor")
			q.Del("contentConversation")
		}
		if pairs[i] == "account" && pairs[i+1] != v.AccountFilter {
			q.Del("contentCursor")
		}
		if pairs[i] == "chat" && pairs[i+1] != v.ChatID {
			q.Del("message")
		}
		if pairs[i+1] == "" {
			q.Del(pairs[i])
		} else {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	if len(q) == 0 {
		return "/"
	}
	return "/?" + q.Encode()
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
	if v.FormTarget == "register" || strings.HasPrefix(v.FormTarget, "connect-") || strings.HasPrefix(v.FormTarget, "disconnect-") {
		return "accounts"
	}
	return ""
}
