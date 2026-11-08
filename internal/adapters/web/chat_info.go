package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (v view) infoTitle() string {
	if v.ConversationInfo != nil && v.ConversationInfo.Name != nil && strings.TrimSpace(*v.ConversationInfo.Name) != "" {
		return *v.ConversationInfo.Name
	}
	if v.Selected != nil {
		return v.Selected.Title
	}
	return ""
}
func (v view) infoHref(screen, kind string, pairs ...string) string {
	base := []string{"open", "details", "infoView", screen, "infoType", kind, "infoCursor", "", "infoQuery", v.InfoQuery, "lang", v.Locale, "theme", v.Theme}
	return v.href(append(base, pairs...)...)
}
func (v view) infoBackHref() string {
	focus := v.InfoType
	if v.InfoView == "stars" {
		focus = "stars"
	}
	return v.infoHref("", "", "infoQuery", "", "infoFocus", focus)
}
func (v view) infoSourceHref(m engineapi.Message) string {
	return v.href("section", "chats", "chat", m.ConversationId, "message", m.Id, "account", m.AccountId, "q", "", "filter", "all", "open", "", "infoView", "", "infoType", "", "infoCursor", "", "infoQuery", "", "infoFocus", "", "lang", v.Locale, "theme", v.Theme)
}
func (v view) infoSharedView() view {
	out := v
	out.SharedInDetails = true
	out.SharedContent = v.InfoShared
	out.SharedType = v.InfoType
	out.SharedCursor = v.InfoCursor
	out.SharedConversation = v.Selected.Id
	out.SharedInvalid = v.InfoInvalid
	out.MessagePreviews = map[string][]engineapi.MessagePreview{}
	return out
}
func (s *Server) loadChatInfo(ctx context.Context, v *view) {
	if v.Selected == nil {
		return
	}
	if v.InfoView == "shared" {
		scoped := v.infoSharedView()
		scoped.AccountFilter = v.Selected.AccountId
		scoped.Query = v.InfoQuery
		s.loadSharedContent(ctx, &scoped)
		v.InfoShared = scoped.SharedContent
		v.InfoInvalid = scoped.SharedInvalid
	}
	if v.InfoView == "stars" {
		limit := 50
		account, conversation := v.Selected.AccountId, v.Selected.Id
		params := &engineapi.ListStarredMessagesParams{AccountId: &account, ConversationId: &conversation, Limit: &limit}
		if v.InfoCursor != "" {
			params.Cursor = &v.InfoCursor
		}
		result, err := s.client.ListStarredMessagesWithResponse(ctx, params)
		if err != nil {
			return
		}
		if result.StatusCode() == 400 || result.StatusCode() == 409 {
			v.InfoInvalid = true
			return
		}
		if result.JSON200 == nil {
			return
		}
		for _, item := range result.JSON200.Items {
			if !validOperationKey(item.Message.Id) || !validOperationKey(item.Message.RevisionId) || item.Conversation.Id != conversation || item.Conversation.AccountId != account || item.Message.ConversationId != conversation || item.Message.AccountId != account || item.Star.MessageId != item.Message.Id || item.Star.AccountId != account || item.Star.ConversationId != conversation || !item.Star.Starred || item.Star.RevisionId == nil || !validOperationKey(*item.Star.RevisionId) {
				return
			}
		}
		v.InfoStars = result.JSON200
	}
}
func (v view) infoStarView() view {
	out := v
	out.Stars = map[string]engineapi.MessageStar{}
	out.StarsKnown = v.InfoStars != nil
	if v.InfoStars != nil {
		for _, item := range v.InfoStars.Items {
			out.Stars[item.Message.Id] = item.Star
		}
	}
	return out
}
func infoExcerpt(m engineapi.Message) string {
	source := m.OriginalText
	if m.Reply != nil {
		source = m.Reply.BodyText
	}
	runes := []rune(source)
	if len(runes) > 512 {
		return string(runes[:512]) + "…"
	}
	return source
}

func (v view) infoStarsVersion() string {
	data, _ := json.Marshal([]any{v.InfoStars, v.InfoInvalid})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
