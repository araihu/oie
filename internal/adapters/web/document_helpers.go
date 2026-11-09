package web

import (
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"net/url"
	"path"
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
