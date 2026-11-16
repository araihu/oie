package web

import (
	"context"
	_ "embed"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

//go:embed message-audio.js
var messageAudioScript []byte

//go:embed audio-duration.js
var audioDurationScript []byte

//go:embed wavesurfer/LICENSE
var waveSurferLicense []byte

// Match the audio types allowed in same-origin browser responses.
func audioMediaType(mediaType string) string {
	switch mediaType {
	case "audio/mpeg", "audio/ogg", "audio/mp4", "audio/wav", "audio/flac", "audio/aac", "audio/webm":
		return mediaType
	case "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "audio/wav"
	case "audio/x-m4a":
		return "audio/mp4"
	case "audio/x-flac":
		return "audio/flac"
	case "audio/opus":
		return "audio/ogg"
	}
	return ""
}

func audioURLMediaType(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".opus", ".oga":
		return "audio/ogg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".aac":
		return "audio/aac"
	case ".weba":
		return "audio/webm"
	}
	return ""
}

func (s *Server) loadMessageAudio(ctx context.Context, v *view) {
	if v.Selected == nil || v.Preview {
		return
	}
	visible := map[string]messageView{}
	ids := []engineapi.ID{}
	for _, message := range v.Messages {
		if message.AudioAttachmentUrls != nil && len(*message.AudioAttachmentUrls) > 0 {
			visible[message.Id] = message
			ids = append(ids, message.Id)
			continue
		}
		for _, match := range messageBodyURLs.FindAllString(message.displayBody(), 128) {
			if audioURLMediaType(strings.TrimRight(match, ".,;!)]}")) != "" {
				visible[message.Id] = message
				ids = append(ids, message.Id)
				break
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	limit, kind := 100, engineapi.ListSharedContentParamsType("audio")
	items := map[string][]engineapi.SharedContentItem{}
	for start := 0; start < len(ids); start += 32 {
		batch := ids[start:min(start+32, len(ids))]
		params := &engineapi.ListSharedContentParams{Type: &kind, Limit: &limit, ConversationId: &v.Selected.Id, AccountId: &v.Selected.AccountId, MessageId: &batch}
		for page := 0; page < 3; page++ {
			response, err := s.client.ListSharedContentWithResponse(ctx, params)
			if err != nil || response.JSON200 == nil {
				return
			}
			for _, item := range response.JSON200.Items {
				message, found := visible[item.Message.Id]
				if !found || item.Kind != "audio" || !validAudioSource(item, message, v.Selected) {
					return
				}
				if len(items[message.Id]) < 3 {
					items[message.Id] = append(items[message.Id], item)
				}
			}
			if response.JSON200.NextCursor == nil || params.Cursor != nil && *params.Cursor == *response.JSON200.NextCursor {
				break
			}
			params.Cursor = response.JSON200.NextCursor
		}
	}
	v.MessageAudio = items
}

func validAudioSource(item engineapi.SharedContentItem, message messageView, owner *engineapi.Conversation) bool {
	return validMessageMedia(item, message, owner) &&
		(hasMediaReference(message.displayBody(), item.Url) || message.AudioAttachmentUrls != nil && slices.Contains(*message.AudioAttachmentUrls, item.Url))
}
