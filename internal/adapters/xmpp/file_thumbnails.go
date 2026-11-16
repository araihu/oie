package xmpp

import (
	"encoding/base64"
	"encoding/hex"
	"mime"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

// HTTP sources, advertised original digests and thumbnail hints from XEP-0447.
// The application binds metadata to exact message URLs before any fetch.
type fileSharingMetadata struct {
	Files []struct {
		File struct {
			Name      string `xml:"urn:xmpp:file:metadata:0 name"`
			MediaType string `xml:"urn:xmpp:file:metadata:0 media-type"`
			Hashes    []struct {
				Algorithm string `xml:"algo,attr"`
				Value     string `xml:",chardata"`
			} `xml:"urn:xmpp:hashes:2 hash"`
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

func decodeFileDigests(meta fileSharingMetadata) []domain.FileDigest {
	var result []domain.FileDigest
	for _, file := range meta.Files {
		for _, hash := range file.File.Hashes {
			if len(hash.Value) > 88 {
				continue
			}
			value, err := base64.StdEncoding.Strict().DecodeString(hash.Value)
			if err != nil {
				continue
			}
			digest := domain.FileDigest{Algorithm: hash.Algorithm, Value: hex.EncodeToString(value)}
			if !digest.Valid() {
				continue
			}
			for _, source := range file.Sources.URLs {
				if len(result) >= 64 {
					return result
				}
				if len(source.Target) <= 4096 {
					digest.URL = source.Target
					result = append(result, digest)
				}
			}
		}
	}
	return result
}

func decodeAudioURLs(meta fileSharingMetadata) []string {
	var urls []string
	for _, file := range meta.Files {
		mediaType, _, _ := mime.ParseMediaType(file.File.MediaType)
		if domain.PlayableAudioType(mediaType) == "" {
			continue
		}
		for _, source := range file.Sources.URLs {
			if len(urls) >= 32 {
				return urls
			}
			if len(source.Target) <= 4096 && (strings.HasPrefix(source.Target, "https://") || strings.HasPrefix(source.Target, "http://")) {
				urls = append(urls, source.Target)
			}
		}
	}
	return urls
}

func decodeFileReferences(meta fileSharingMetadata) []domain.SharedReference {
	var refs []domain.SharedReference
	for _, file := range meta.Files {
		mediaType, _, _ := mime.ParseMediaType(file.File.MediaType)
		if domain.PlayableAudioType(mediaType) != "" {
			continue
		}
		name := path.Base(strings.ReplaceAll(file.File.Name, "\\", "/"))
		var kind string
		switch mediaType {
		case "image/png", "image/jpeg", "image/gif":
			kind = "image"
		case "video/mp4", "video/quicktime", "video/webm", "video/x-matroska", "video/x-msvideo":
			kind = "video"
		case "application/pdf":
			kind = "document"
			if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
				name += ".pdf"
			}
		case "text/plain":
			kind = "document"
			if path.Ext(name) == "" {
				name += ".txt"
			}
		}
		for _, source := range file.Sources.URLs {
			if len(refs) >= 32 {
				return refs
			}
			if len(source.Target) > 4096 {
				continue
			}
			u, err := url.Parse(source.Target)
			if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
				continue
			}
			sourceKind := kind
			if sourceKind == "" {
				if matches := application.SharedReferences(source.Target); len(matches) == 1 && matches[0].URL == source.Target {
					sourceKind = matches[0].Kind
				}
			}
			if sourceKind != "" && sourceKind != "link" {
				refs = append(refs, domain.SharedReference{URL: source.Target, Kind: sourceKind, FileName: name})
			}
		}
	}
	return refs
}

func attachmentBodyFallback(body string, meta fileSharingMetadata) string {
	if strings.TrimSpace(body) != "" {
		return body
	}
	var addresses []string
	for _, file := range meta.Files {
		for _, source := range file.Sources.URLs {
			if len(source.Target) > 4096 || strings.ContainsAny(source.Target, "\r\n\t <>\"") {
				continue
			}
			u, err := url.Parse(source.Target)
			if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || slices.Contains(addresses, source.Target) {
				continue
			}
			addresses = append(addresses, source.Target)
			break
		}
		if len(addresses) == 4 {
			break
		}
	}
	return strings.Join(addresses, "\n")
}
