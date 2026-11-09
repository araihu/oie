package web

import (
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"net/url"
	"path"
	"regexp"
	"strings"
)

func documentFormat(item engineapi.SharedContentItem) string {
	address, err := url.Parse(item.Url)
	extension := ""
	if err == nil {
		extension = strings.ToLower(path.Ext(address.Path))
	}
	if extension == "" {
		extension = strings.ToLower(path.Ext(item.FileName))
	}
	switch extension {
	case ".pdf":
		return "pdf"
	case ".md":
		return "markdown"
	case ".txt", ".log", ".csv", ".json", ".xml":
		return "text"
	default:
		return "unsupported"
	}
}
func documentSource(v view, item engineapi.SharedContentItem) string {
	if !validOperationKey(v.EngineID) || !validOperationKey(item.Id) {
		return ""
	}
	return "/ui/shared-content/" + item.Id + "/content?engineId=" + v.EngineID
}

var documentCaptionURLs = regexp.MustCompile("https?://[^\\s<>\"`]+")

// The catalog excerpt is a bounded presentation of the source message. It does
// not change retained text, and a file reference alone is not a caption.
func documentCaption(item engineapi.SharedContentItem) string {
	body := (messageView{Message: item.Message}).displayBody()
	body = strings.ReplaceAll(body, "](<"+item.Url+">)", "]")
	body = strings.ReplaceAll(body, "]("+item.Url+")", "]")
	body = strings.ReplaceAll(body, "<"+item.Url+">", "")
	body = documentCaptionURLs.ReplaceAllStringFunc(body, func(reference string) string {
		if strings.TrimRight(reference, ".,;!)]}") == item.Url {
			return strings.TrimPrefix(reference, item.Url)
		}
		return reference
	})
	lines := []string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		bare := strings.Trim(line, " \t<>[]().,;!")
		if bare == "" || bare == item.FileName {
			continue
		}
		lines = append(lines, line)
	}
	caption := strings.Join(lines, "\n")
	if runes := []rune(caption); len(runes) > 600 {
		return string(runes[:600]) + "…"
	}
	return caption
}
