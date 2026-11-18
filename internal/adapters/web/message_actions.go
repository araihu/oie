package web

import (
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
func messageActions(v view, message messageView) []dropdown.Section {
	id := message.Id
	sections := []dropdown.Section{{Items: []dropdown.Item{
		{ID: "copy-message-" + id, Icon: glyph(heroicons.Icon16SolidDocumentDuplicate), Label: v.Text.CopyOriginal, OnClick: "void 0"},
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
