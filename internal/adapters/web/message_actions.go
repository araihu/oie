package web

import (
	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/icon/heroicons"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
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
func messageActions(v view, id string) []dropdown.Section {
	sections := []dropdown.Section{{Items: []dropdown.Item{
		{ID: "copy-message-" + id, Icon: glyph(heroicons.Icon16SolidDocumentDuplicate), Label: v.Text.CopyOriginal, OnClick: "void 0"},
		{ID: "star-message-" + id, Icon: glyph(heroicons.Icon16SolidStar), Label: v.starLabel(id), OnClick: "void 0", Disabled: !v.StarsKnown, Tooltip: v.Text.StarUnavailable},
		{ID: "reply-message-" + id, Icon: glyph(heroicons.Icon16SolidArrowUturnLeft), Label: v.Text.ReplyMessage, OnClick: "void 0", Disabled: !v.replyAllowed(), Tooltip: v.Text.ReplyUnavailable},
	}}}
	if _, known := v.Reactions[id]; known {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "react-message-" + id, Icon: glyph(heroicons.Icon16SolidFaceSmile), Label: v.Text.ReactMessage, OnClick: "void 0", Disabled: !v.reactionAllowed(id), Tooltip: v.Text.ReactionUnavailable}}})
	}
	if v.editableMessage(id) {
		sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "edit-message-" + id, Icon: glyph(heroicons.Icon16SolidPencilSquare), Label: v.Text.EditMessage, Href: v.editTargetHref(id)}}})
	}
	for _, message := range v.Messages {
		if message.Id == id && message.IsOwn && pendingSendMutable(message.Message) && !v.Preview {
			sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "cancel-message-" + id, Label: v.Text.CancelSend, Icon: glyph(heroicons.Icon16SolidXMark), OnClick: "$dispatch('oie:cancel-message', {id: '" + id + "'})"}}})
			break
		}
	}
	for _, message := range v.Messages {
		if message.Id == id && messageEdited(message) {
			sections = append(sections, dropdown.Section{Items: []dropdown.Item{{ID: "history-message-" + id, Icon: glyph(heroicons.Icon16SolidClock), Label: v.Text.ViewEditHistory, Href: v.editHistoryHref(message)}}})
			break
		}
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
	return sections
}
func imageActions(v view, p engineapi.MessagePreview) []dropdown.Section {
	return []dropdown.Section{{Items: []dropdown.Item{
		{ID: "open-preview-" + p.Id, Icon: glyph(heroicons.Icon16SolidMagnifyingGlass), Label: v.Text.OpenImage, OnClick: "void 0"},
		{ID: "info-preview-" + p.Id, Icon: glyph(heroicons.Icon16SolidInformationCircle), Label: v.Text.ImageInfo, OnClick: "void 0"},
		{ID: "save-preview-" + p.Id, Icon: glyph(heroicons.Icon16SolidArrowDownTray), Label: v.Text.SaveOriginalImage, OnClick: "void 0", Disabled: mediaSource(p.FileHash) == ""},
	}}}
}
