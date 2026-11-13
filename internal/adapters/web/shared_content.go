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
	if len(v.SharedAccountIDs) > 0 {
		config := v.searchableAccountCombobox("shared-account", "contentAccount", v.SharedAccountIDs)
		config.RootClass = "oie-shared-account-filter"
		return config
	}
	options := make([]combobox.Option, 0, len(v.Accounts)+1)
	for _, option := range accountOptions(v, true) {
		options = append(options, combobox.Option{Value: option.Value, Label: option.Label})
	}
	return combobox.Config{
		ID: "shared-account", Name: "account", Label: v.Text.SharedAccount,
		RootClass: "oie-shared-account-filter", Mode: combobox.ModeSingle,
		Source: combobox.Source{Static: options}, Selected: []string{v.AccountFilter},
		EnableSearch: true, DisablePersistence: true,
	}
}

const sharedContentPageSize = 50

func (s *Server) loadSharedContent(ctx context.Context, v *view, includeFirstPageCounts bool) {
	limit := sharedContentPageSize
	kind := engineapi.ListSharedContentParamsType(v.SharedType)
	params := &engineapi.ListSharedContentParams{Type: &kind, Limit: &limit}
	if includeFirstPageCounts {
		params.IncludeFirstPageCounts = &includeFirstPageCounts
	}
	if v.AccountFilter != "" {
		params.AccountId = &v.AccountFilter
	}
	if len(v.SharedAccountIDs) > 0 {
		params.AccountIds = &v.SharedAccountIDs
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
	for _, item := range result.JSON200.Items {
		m := item.Message
		if !validOperationKey(item.Id) || !validOperationKey(m.Id) || !validOperationKey(m.AccountId) || !validOperationKey(m.ConversationId) || safePreviewURL(item.Url) == "" || v.account(m.AccountId).Address == "" {
			return
		}
		if (v.AccountFilter != "" && m.AccountId != v.AccountFilter) || (len(v.SharedAccountIDs) > 0 && !slices.Contains(v.SharedAccountIDs, m.AccountId)) || (v.SharedConversation != "" && m.ConversationId != v.SharedConversation) {
			return
		}
		if (v.SharedType == "links" && item.Kind != "link") || (v.SharedType == "documents" && item.Kind != "document") || (v.SharedType == "media" && item.Kind != "image" && item.Kind != "audio" && item.Kind != "video") {
			return
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

// Preview metadata refreshes independently from the canonical catalog item.
func sharedItemVersion(item engineapi.SharedContentItem) string {
	item.Preview = nil
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
