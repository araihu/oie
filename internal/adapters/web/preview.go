package web

import (
	_ "embed"
	"github.com/araihu/oie/internal/adapters/previewapi"
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
	v.Query = q.Get("q")
	if query := []rune(v.Query); len(query) > 200 {
		v.Query = string(query[:200])
	}
}
func (v view) href(pairs ...string) string {
	q := url.Values{}
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
	}
	for i := 0; i+1 < len(pairs); i += 2 {
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

func chatInitials(c previewapi.Conversation) string {
	if c.Initials != nil {
		return *c.Initials
	}
	return initials(c.Title)
}
func participantNames(c previewapi.Conversation) []string {
	if c.Participants != nil {
		return *c.Participants
	}
	return []string{c.Address}
}
