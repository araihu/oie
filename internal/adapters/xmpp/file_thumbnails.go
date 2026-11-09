package xmpp

import (
	"github.com/araihu/oie/internal/domain"
	"strings"
)

// Retain the HTTP thumbnail subset of XEP-0447/0446/0264. The application
// binds hints to current body references; the fetch adapter validates destinations.
// CID/BoB and file-only messages without a body remain outside this increment.
type fileSharingMetadata struct {
	Files []struct {
		File struct {
			MediaType  string `xml:"urn:xmpp:file:metadata:0 media-type"`
			Thumbnails []struct {
				URI       string `xml:"uri,attr"`
				MediaType string `xml:"media-type,attr"`
			} `xml:"urn:xmpp:thumbs:1 thumbnail"`
		} `xml:"urn:xmpp:file:metadata:0 file"`
		Sources struct {
			URLs []struct {
				Target string `xml:"target,attr"`
			} `xml:"http://jabber.org/protocol/url-data url-data"`
		} `xml:"urn:xmpp:sfs:0 sources"`
	} `xml:"urn:xmpp:sfs:0 file-sharing"`
}

func decodeFileThumbnails(meta fileSharingMetadata) []domain.MediaThumbnail {
	var result []domain.MediaThumbnail
	for _, file := range meta.Files {
		if !strings.HasPrefix(file.File.MediaType, "video/") {
			continue
		}
		for _, thumb := range file.File.Thumbnails {
			if len(thumb.URI) > 4096 || (!strings.HasPrefix(thumb.URI, "https://") && !strings.HasPrefix(thumb.URI, "http://")) {
				continue
			}
			if thumb.MediaType != "image/png" && thumb.MediaType != "image/jpeg" && thumb.MediaType != "image/gif" {
				continue
			}
			for _, source := range file.Sources.URLs {
				if len(result) >= 4 {
					return result
				}
				if len(source.Target) <= 4096 {
					result = append(result, domain.MediaThumbnail{URL: source.Target, ThumbnailURL: thumb.URI})
				}
			}
			break
		}
	}
	return result
}
