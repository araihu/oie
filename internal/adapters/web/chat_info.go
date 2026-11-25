package web

import (
	"context"
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
	base := []string{"chat", v.ChatID, "open", "details", "infoView", screen, "infoType", kind, "infoCursor", "", "infoQuery", v.InfoQuery, "lang", v.Locale, "theme", v.Theme}
	return v.href(append(base, pairs...)...)
}
func (v view) infoBackHref() string {
	if v.InfoView == "retention" || v.InfoView == "read-receipts" {
		return v.infoHref("preferences", "", "infoFocus", v.InfoView)
	}
	focus := v.InfoType
	if v.InfoView == "stars" || v.InfoView == "participants" || v.InfoView == "preferences" || v.InfoView == "connection" {
		focus = v.InfoView
	}
	return v.infoHref("", "", "infoQuery", "", "infoIncludeRetracted", "", "infoFocus", focus)
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
	out.SharedIncludeRetracted = v.InfoIncludeRetracted
	out.SharedConversation = v.Selected.Id
	out.SharedInvalid = v.InfoInvalid
	out.MessagePreviews = map[string][]engineapi.MessagePreview{}
	return out
}
func (s *Server) loadChatInfo(ctx context.Context, v *view, refresh bool) {
	if v.Selected == nil {
		return
	}
	if v.InfoView == "participants" {
		if v.Selected.Kind == "room" && v.requestKind() == "" && !v.Preview {
			result, err := s.client.GetRoomParticipantsWithResponse(ctx, v.Selected.Id)
			if err == nil && result.JSON200 != nil && result.JSON200.State.Valid() {
				v.InfoParticipants = result.JSON200
			}
		}
		return
	}
	if v.InfoView == "shared" {
		scoped := v.infoSharedView()
		scoped.AccountFilter = v.Selected.AccountId
		scoped.Query = v.InfoQuery
		s.loadSharedContent(ctx, &scoped, scoped.SharedCursor == "" || refresh)
		v.InfoShared = scoped.SharedContent
		v.InfoInvalid = scoped.SharedInvalid
	}
	if v.InfoView == "stars" {
		v.InfoStars, v.InfoInvalid = s.loadStarredMessages(ctx, v.Selected.AccountId, v.Selected.Id, v.InfoCursor, "")
		s.loadStarredContent(ctx, v, v.InfoStars)
	}
}
