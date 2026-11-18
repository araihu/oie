package web

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/icon/heroicons"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/web/appicons"
)

func (v view) imagePreviews(id string) []engineapi.MessagePreview {
	var images []engineapi.MessagePreview
	for _, p := range v.MessagePreviews[id] {
		if p.Kind == "image" {
			images = append(images, p)
		}
	}
	return images
}

func offlineMenuSections(v view) []dropdown.Section {
	return []dropdown.Section{{Items: []dropdown.Item{
		{ID: "copy-message-oie-message-placeholder", Icon: glyph(heroicons.Icon16SolidDocumentDuplicate), Label: v.Text.CopyOriginal, OnClick: "void 0"},
		{ID: "retry-menu-oie-message-placeholder", Label: v.Text.Retry, OnClick: "void 0"},
	}}}
}

func (v view) messageMenuURL(m messageView) string {
	q := url.Values{"engineId": {v.EngineID}, "conversationId": {m.ConversationId}, "accountId": {m.AccountId}, "messageId": {m.Id}, "lang": {v.Locale}, "theme": {v.Theme}}
	return "/ui/message-menu?" + q.Encode()
}

func (s *Server) serveMessageMenu(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	engine, account, chat, id := q.Get("engineId"), q.Get("accountId"), q.Get("conversationId"), q.Get("messageId")
	if !validOperationKey(engine) || !validOperationKey(account) || !validOperationKey(chat) || !validOperationKey(id) {
		w.WriteHeader(400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	v := s.baseView(r)
	current, err := s.client.GetEngineWithResponse(ctx)
	if err != nil || current.JSON200 == nil {
		w.WriteHeader(503)
		return
	}
	if current.JSON200.Id != engine {
		w.WriteHeader(409)
		return
	}
	v.EngineID, v.ChatID = engine, chat
	v.Preview = current.HTTPResponse.Header.Get("X-Oie-Mock") == "true"
	owner, err := s.client.GetConversationWithResponse(ctx, chat)
	if err != nil {
		w.WriteHeader(503)
		return
	}
	if owner.JSON200 == nil {
		if owner.StatusCode() == 404 {
			w.WriteHeader(404)
		} else {
			w.WriteHeader(503)
		}
		return
	}
	if owner.JSON200.Id != chat || owner.JSON200.AccountId != account || owner.JSON200.Request != nil {
		w.WriteHeader(404)
		return
	}
	v.Selected = owner.JSON200
	var m *engineapi.Message
	if v.Preview {
		// The read-only preview API exposes fixture messages through its list.
		page, err := s.client.ListMessagesWithResponse(ctx, chat, timelineInitialParams(v))
		if err != nil || page.JSON200 == nil {
			w.WriteHeader(503)
			return
		}
		for _, item := range page.JSON200.Items {
			if item.Id == id && item.AccountId == account && item.ConversationId == chat {
				m = &item
				break
			}
		}
	} else {
		var status int
		m, status = s.retainedReplySource(ctx, chat, account, id)
		if m == nil {
			w.WriteHeader(status)
			return
		}
	}
	if m == nil || m.Id != id || m.Moderation != nil || m.Retraction != nil {
		w.WriteHeader(404)
		return
	}
	if owner.JSON200.Kind == "room" {
		s.loadConversationDetails(ctx, &v)
	}
	if !v.Preview {
		a, err := s.client.GetAccountWithResponse(ctx, account)
		if err == nil && a.JSON200 != nil && a.JSON200.Id == account {
			v.Accounts = []engineapi.Account{*a.JSON200}
		}
	}
	s.loadMessaging(ctx, &v)
	if m.IsOwn {
		s.latestEditTarget(ctx, &v)
	}
	v.setTimelineMessages([]engineapi.Message{*m}, nil)
	s.loadMessagePreviews(ctx, &v)
	s.loadMessageVideos(ctx, &v)
	s.loadMessageAudio(ctx, &v)
	s.loadMessageStars(ctx, &v)
	s.loadReactions(ctx, &v)
	menu := dropdown.Dropdown(dropdown.Config{ID: "message-menu-" + id, Label: v.Text.MessageActions, Trigger: messageMenuTrigger(v, id), MenuAlign: dropdown.AlignEnd, Sections: messageActions(v, v.Messages[0])})
	var body bytes.Buffer
	if err := menu.Render(presentationContext(ctx, v), &body); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	body = encodeFragment(w, r, body)
	_, _ = body.WriteTo(w)
}

func messageActions(v view, message messageView) []dropdown.Section {
	id := message.Id
	sections := []dropdown.Section{{Items: []dropdown.Item{
		{ID: "star-message-" + id, Icon: glyph(heroicons.Icon16SolidStar), Label: v.starLabel(id), OnClick: "void 0", Disabled: !v.StarsKnown, Tooltip: v.Text.StarUnavailable},
		{ID: "reply-message-" + id, Icon: glyph(heroicons.Icon16SolidArrowUturnLeft), Label: v.Text.ReplyMessage, OnClick: "void 0", Disabled: !v.replyAllowed(), Tooltip: v.Text.ReplyUnavailable},
	}}}
	if !v.Preview && message.SendState != "cancelled" {
		sections[0].Items = append(sections[0].Items, forwardAction(v, "", id))
	}
	if _, known := v.Reactions[id]; known {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "react-message-" + id, Icon: glyph(heroicons.Icon16SolidFaceSmile), Label: v.Text.ReactMessage, OnClick: "void 0", Disabled: !v.reactionAllowed(id), Tooltip: v.Text.ReactionUnavailable}}})
	}
	if v.editableMessage(id) {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{
			ID:    "edit-message-" + id,
			Icon:  appicons.Icon(appicons.Config{Symbol: appicons.IconHeroiconsPencil16SolidPencil, Decorative: true, RootClass: "oie-icon"}),
			Label: v.Text.EditMessage,
			Href:  v.editTargetHref(id),
		}}})
	}
	if !v.Preview && message.CanRetract != nil && *message.CanRetract {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "retract-message-" + id, Icon: glyph(heroicons.Icon16SolidTrash), Label: v.Text.RetractMessage, OnClick: "void 0"}}})
	}
	if v.Selected != nil && v.Selected.Kind == "room" && !v.Preview &&
		!message.IsOwn && message.Moderation == nil && v.roomState() == "joined" &&
		v.ConversationInfo != nil && v.ConversationInfo.CanModerateMessages != nil && *v.ConversationInfo.CanModerateMessages {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "moderate-room-message-" + id, Icon: glyph(heroicons.Icon16SolidTrash), Label: v.Text.RoomModerateMessage, OnClick: "void 0"}}})
	}
	if message.IsOwn && pendingSendMutable(message.Message) && !v.Preview {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "cancel-message-" + id, Label: v.Text.CancelSend, Icon: glyph(heroicons.Icon16SolidXMark), OnClick: "$dispatch('oie:cancel-message', {id: '" + id + "'})"}}})
	}
	if messageEdited(message) {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "history-message-" + id, Icon: historyIcon(), Label: v.Text.ViewEditHistory, Href: v.editHistoryHref(message)}}})
	}
	if images := v.imagePreviews(id); len(images) == 1 {
		sections = append(sections, imageActions(v, images[0])...)
	}
	if videos := v.messageVideos(id); len(videos) > 0 {
		items := make([]dropdown.Item, 0, len(videos))
		for _, video := range videos {
			label := v.Text.SharedOpenLink
			if len(videos) > 1 {
				label += " · " + v.sharedTitle(video)
			}
			items = append(items, dropdown.Item{ID: "open-video-" + video.Id, Icon: glyph(heroicons.Icon16SolidArrowTopRightOnSquare), Label: label, Href: safePreviewURL(video.Url), Target: "_blank", Rel: "noopener noreferrer"})
		}
		sections = append(sections, dropdown.Section{Items: items})
	}
	if audio := audioDownloadActions(v, message); len(audio) > 0 {
		sections = append(sections, dropdown.Section{Items: audio})
	}
	return sections
}

func audioDownloadActions(v view, message messageView) []dropdown.Item {
	var items []dropdown.Item
	item := func(id, name string) dropdown.Item {
		return dropdown.Item{ID: "save-audio-" + id, Label: name, Icon: glyph(heroicons.Icon16SolidArrowDownTray), OnClick: "void 0"}
	}
	for _, audio := range v.MessageAudio[message.Id] {
		items = append(items, item(audio.Id, v.sharedTitle(audio)))
	}
	if message.Attachments != nil {
		for _, audio := range *message.Attachments {
			if audioMediaType(audio.MediaType) != "" {
				items = append(items, item(audio.Id, audio.FileName))
			}
		}
	}
	for i := range items {
		if len(items) == 1 {
			items[i].Label = v.Text.AudioDownload
		} else {
			items[i].Label = v.Text.AudioDownload + " · " + items[i].Label
		}
	}
	return items
}

func imageActions(v view, p engineapi.MessagePreview) []dropdown.Section {
	return []dropdown.Section{{Items: []dropdown.Item{
		{ID: v.previewControlID("open-preview", p.Id), Icon: glyph(heroicons.Icon16SolidMagnifyingGlass), Label: v.Text.OpenImage, OnClick: "void 0"},
		{ID: v.previewControlID("info-preview", p.Id), Icon: glyph(heroicons.Icon16SolidInformationCircle), Label: v.Text.ImageInfo, OnClick: "void 0"},
		{ID: v.previewControlID("save-preview", p.Id), Icon: glyph(heroicons.Icon16SolidArrowDownTray), Label: v.Text.SaveOriginalImage, OnClick: "void 0", Disabled: mediaSource(p.FileHash) == ""},
	}}}
}
