package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// A plain received message can change these summary fields. All other
// conversation changes require the ordinary header, composer and details refresh.
func (v view) appendScope() string {
	if v.Selected == nil || v.Selected.Kind != "direct" || v.Selected.Request != nil || v.conversationRemoved() {
		return ""
	}
	c := *v.Selected
	c.LastMessage, c.RevisionId = "", ""
	c.UpdatedAt = time.Time{}
	c.UnreadCount = 0
	c.LatestMessagePageCount = nil
	data, _ := json.Marshal(c)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func plainIncoming(m engineapi.Message) bool {
	return !m.IsOwn && m.SendState == "received" && m.OriginalText != "" &&
		(m.Historical == nil || !*m.Historical) && (m.Edited == nil || !*m.Edited) &&
		m.Retraction == nil && m.Moderation == nil && m.Reply == nil && m.ReplyToMessageId == nil &&
		m.Attachments == nil && m.AudioAttachmentUrls == nil && m.VideoAttachmentUrls == nil &&
		m.Composition == nil && m.ScheduledAt == nil && m.Mentions == nil &&
		!strings.Contains(m.OriginalText, "://") && !strings.Contains(strings.ToLower(m.OriginalText), "xmpp:")
}

func (s *Server) loadLiveMessage(ctx context.Context, r *http.Request, v *view, editor engineapi.RequestEditorFn) bool {
	id, after := r.Header.Get("X-Oie-Timeline-Message"), r.Header.Get("X-Oie-Timeline-After")
	scope := r.Header.Get("X-Oie-Timeline-Scope")
	if !validOperationKey(id) || !validOperationKey(after) || scope == "" ||
		v.ChatID == "" || v.RefreshRow != v.ChatID || v.Preview || v.FocusMessage != "" || v.InfoView != "" || v.TimelineCursor != "" {
		return false
	}
	part := *v
	for i := range v.Chats {
		if v.Chats[i].Id == v.ChatID {
			part.Selected = &v.Chats[i]
			break
		}
	}
	if part.appendScope() != scope {
		return false
	}
	limit, include := 2, true
	params := &engineapi.ListMessagesParams{Limit: &limit}
	if v.PagePresentationSupported {
		params.IncludePresentation = &include
	}
	response, err := s.client.ListMessagesWithResponse(ctx, v.ChatID, params, editor)
	if err != nil || response.JSON200 == nil {
		return false
	}
	page := response.JSON200
	if !validTimelinePage(*page, part.Selected.AccountId, v.ChatID, "older", nil, limit) || len(page.Items) != 2 ||
		page.Items[0].Id != id || page.Items[1].Id != after || !plainIncoming(page.Items[0]) {
		return false
	}
	part.MessagePreviews = map[string][]engineapi.MessagePreview{}
	if page.Presentation != nil {
		part.setTimelineMessages(page.Items[:1], nil)
		part.applyTimelinePresentation(page.Presentation)
		part.formatTimelineMessages(ctx)
	} else {
		s.formatTimeline(ctx, &part, page.Items[:1], nil)
	}
	m := part.Messages[0]
	if !part.StarsKnown || !part.PreviewsKnown || !m.VideosKnown || len(part.MessagePreviews[id])+len(part.messageVideos(id))+len(part.MessageAudio[id])+len(m.RoomInvitations) > 0 {
		return false
	}
	if _, ok := part.Reactions[id]; !ok {
		return false
	}
	part.AppendAfter = after
	part.AppendGrouped = messagesGrouped(messageView{Message: page.Items[1]}, m)
	*v = part
	return true
}
