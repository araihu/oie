package web

import (
	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/icon/heroicons"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func sharedActionScope(v view) string {
	if v.SharedInDetails {
		return "info"
	}
	return "global"
}

func sharedActionID(v view, item engineapi.SharedContentItem, action string) string {
	return "shared-" + action + "-" + sharedActionScope(v) + "-" + item.Id
}

func sharedActionPreview(item engineapi.SharedContentItem) string {
	if item.Preview != nil && validOperationKey(item.Preview.Id) {
		return item.Preview.Id
	}
	return ""
}

func sharedCardView(v view) view {
	v.SharedCard = true
	return v
}

func sharedActionFile(item engineapi.SharedContentItem) string {
	if item.Preview != nil && item.Preview.Kind == "image" {
		return mediaSource(item.Preview.FileHash)
	}
	return ""
}

func sharedActionDownload(v view, item engineapi.SharedContentItem) string {
	if item.Kind == "video" || item.Kind == "document" && documentFormat(item) != "unsupported" {
		return documentSource(v, item)
	}
	return ""
}

func sharedActions(v view, item engineapi.SharedContentItem) []dropdown.Section {
	items := []dropdown.Item{
		{ID: sharedActionID(v, item, "source"), Label: v.Text.SharedGoToMessage, Href: v.sharedSourceHref(item), Icon: glyph(heroicons.Icon16SolidArrowUturnLeft)},
		{ID: sharedActionID(v, item, "copy"), Label: v.Text.SharedCopyLink, OnClick: "void 0", Icon: glyph(heroicons.Icon16SolidDocumentDuplicate)},
		{ID: sharedActionID(v, item, "open"), Label: v.Text.SharedOpenLink, Href: safePreviewURL(item.Url), Target: "_blank", Rel: "noopener noreferrer", Icon: glyph(heroicons.Icon16SolidArrowTopRightOnSquare)},
	}
	if item.Kind == "image" && (sharedActionPreview(item) != "" || sharedActionFile(item) != "") || sharedActionDownload(v, item) != "" {
		items = append(items, dropdown.Item{ID: sharedActionID(v, item, "download"), Label: v.Text.SharedDownload, OnClick: "void 0", Icon: glyph(heroicons.Icon16SolidArrowDownTray)})
	}
	if item.Kind == "link" && sharedActionPreview(item) != "" {
		items = append(items, dropdown.Item{ID: sharedActionID(v, item, "refresh"), Label: v.Text.RefreshPreview, OnClick: "void 0", Icon: glyph(heroicons.Icon16SolidArrowPath), Disabled: item.Preview.State == "queued"})
	}
	if item.Kind == "video" && videoThumbnailState(item) != "" && videoPoster(item) == "" {
		items = append(items, dropdown.Item{ID: sharedActionID(v, item, "thumbnail"), Label: videoThumbnailActionLabel(v, item), OnClick: "void 0", Icon: glyph(heroicons.Icon16SolidArrowPath)})
	}
	return []dropdown.Section{{Items: items}}
}
