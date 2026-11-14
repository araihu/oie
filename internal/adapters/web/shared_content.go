package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/araihu/goshtoso/components/combobox"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func sharedAccountCombobox(v view) combobox.Config {
	config := v.searchableAccountCombobox("shared-account", "contentAccount", v.sharedCatalogAccounts())
	config.RootClass = "oie-shared-account-filter"
	return config
}

func (v view) sharedCatalogAccounts() []string {
	if v.SharedAccountIDs == nil && v.AccountFilter != "" {
		return []string{v.AccountFilter}
	}
	return v.SharedAccountIDs
}

const sharedContentPageSize = 50

func (s *Server) loadSharedContent(ctx context.Context, v *view, includeFirstPageCounts bool) {
	limit := sharedContentPageSize
	kind := engineapi.ListSharedContentParamsType(v.SharedType)
	includeStars := !v.Preview
	params := &engineapi.ListSharedContentParams{Type: &kind, Limit: &limit, IncludeMessageStars: &includeStars}
	if includeFirstPageCounts {
		params.IncludeFirstPageCounts = &includeFirstPageCounts
	}
	accounts := v.sharedCatalogAccounts()
	switch len(accounts) {
	case 0:
	case 1:
		params.AccountId = &accounts[0]
	default:
		params.AccountIds = &accounts
	}
	if v.SharedConversation != "" {
		params.ConversationId = &v.SharedConversation
	}
	if v.Query != "" {
		params.Q = &v.Query
	}
	if v.SharedCursor != "" {
		params.Cursor = &v.SharedCursor
	}
	result, err := s.client.ListSharedContentWithResponse(ctx, params)
	if err != nil {
		return
	}
	if result.StatusCode() == 400 {
		v.SharedInvalid = true
		return
	}
	if result.JSON200 == nil {
		return
	}
	for i := range result.JSON200.Items {
		item := &result.JSON200.Items[i]
		m := item.Message
		if !validOperationKey(item.Id) || !validOperationKey(m.Id) || !validOperationKey(m.AccountId) || !validOperationKey(m.ConversationId) || safePreviewURL(item.Url) == "" || v.account(m.AccountId).Address == "" {
			return
		}
		if (len(accounts) > 0 && !slices.Contains(accounts, m.AccountId)) || (v.SharedConversation != "" && m.ConversationId != v.SharedConversation) {
			return
		}
		if (v.SharedType == "links" && item.Kind != "link") || (v.SharedType == "documents" && item.Kind != "document") || (v.SharedType == "media" && item.Kind != "image" && item.Kind != "audio" && item.Kind != "video") {
			return
		}
		if item.Star != nil && (v.Preview || item.Star.MessageId != m.Id || !validTimelineStar(*item.Star, m.AccountId, m.ConversationId)) {
			item.Star = nil
		}
		if p := item.Thumbnail; p != nil {
			if item.Kind != "video" || !validVideoThumbnailStatus(*p, item.Id) || p.AccountId != m.AccountId || p.ConversationId != m.ConversationId || p.MessageId != m.Id || p.Url != item.Url {
				return
			}
		}
		if p := item.Preview; p != nil {
			if p.MessageId != m.Id || p.AccountId != m.AccountId || p.ConversationId != m.ConversationId || p.Url != item.Url || ((item.Kind != "image" || p.Kind != "image") && (item.Kind != "link" || p.Kind != "link")) {
				return
			}
		}
	}
	v.SharedContent = result.JSON200
}
func (v view) sharedHref(pairs ...string) string {
	if v.SharedInDetails {
		args := []string{}
		for i := 0; i+1 < len(pairs); i += 2 {
			switch pairs[i] {
			case "contentType":
				args = append(args, "infoType", pairs[i+1])
			case "contentCursor":
				args = append(args, "infoCursor", pairs[i+1])
			}
		}
		return v.infoHref("shared", v.SharedType, args...)
	}
	base := []string{"section", "media", "chat", "", "message", "", "contentType", v.SharedType, "contentConversation", v.SharedConversation, "contentCursor", v.SharedCursor, "lang", v.Locale, "theme", v.Theme}
	return v.href(append(base, pairs...)...)
}
func (v view) sharedSourceHref(item engineapi.SharedContentItem) string {
	if v.SharedInDetails {
		return v.infoSourceHref(item.Message)
	}
	return v.href("section", "chats", "chat", item.Message.ConversationId, "message", item.Message.Id, "account", item.Message.AccountId, "q", "", "filter", "all", "contentType", "", "contentCursor", "", "contentConversation", "", "lang", v.Locale, "theme", v.Theme)
}

// Preview metadata and stars refresh independently from the canonical catalog item.
func sharedItemVersion(item engineapi.SharedContentItem) string {
	item.Preview = nil
	item.Star = nil
	data, _ := json.Marshal(item)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (v view) sharedVersion() string {
	if v.SharedContent == nil {
		return "unavailable"
	}
	items := append([]engineapi.SharedContentItem(nil), v.SharedContent.Items...)
	for i := range items {
		items[i].Preview = nil
		items[i].Thumbnail = nil
		items[i].Star = nil
	}
	data, _ := json.Marshal(items)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func (v view) sharedKindLabel(kind string) string {
	switch kind {
	case "image":
		return v.Text.SharedImage
	case "audio":
		return v.Text.SharedAudio
	case "video":
		return v.Text.SharedVideo
	case "document":
		return v.Text.SharedDocument
	default:
		return v.Text.SharedLink
	}
}
func (v view) sharedAccountLabel(item engineapi.SharedContentItem) string {
	if item.AccountLabel != "" {
		return item.AccountLabel
	}
	return v.accountLabel(item.Message.AccountId)
}
func (v view) sharedTitle(item engineapi.SharedContentItem) string {
	if item.FileName != "" {
		return item.FileName
	}
	return previewHost(engineapi.MessagePreview{Url: item.Url})
}
func (v view) sharedSenderAddress(item engineapi.SharedContentItem) string {
	if item.Message.IsOwn {
		return v.account(item.Message.AccountId).Address
	}
	return ""
}
