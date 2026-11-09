package web

import "github.com/araihu/oie/internal/adapters/httpapi/engineapi"

func videoThumbnailPreview(item engineapi.SharedContentItem) *engineapi.VideoThumbnail {
	p := item.Thumbnail
	if p == nil || !validVideoThumbnailStatus(*p, item.Id) || p.AccountId != item.Message.AccountId || p.ConversationId != item.Message.ConversationId || p.MessageId != item.Message.Id || p.Url != item.Url {
		return nil
	}
	return p
}
func videoPoster(item engineapi.SharedContentItem) string {
	p := videoThumbnailPreview(item)
	if p == nil || p.State != "ready" {
		return ""
	}
	return mediaSource(p.ImageHash)
}
func videoThumbnailState(item engineapi.SharedContentItem) string {
	p := videoThumbnailPreview(item)
	if p == nil {
		return ""
	}
	return string(p.State)
}

func videoThumbnailActionLabel(v view, item engineapi.SharedContentItem) string {
	if videoThumbnailState(item) == "unavailable" {
		return v.Text.Retry
	}
	return v.Text.VideoThumbnailLoad
}
