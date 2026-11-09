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
	if images := v.imagePreviews(id); len(images) == 1 {
		sections = append(sections, imageActions(v, images[0])...)
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
