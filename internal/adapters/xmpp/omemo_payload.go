package xmpp

import (
	"strings"
	"unicode"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
)

const omemoMediaScheme = "aesgcm:"

func readOMEMOPayload(in *application.Incoming, text string) bool {
	if !strings.Contains(strings.ToLower(text), omemoMediaScheme) {
		in.OriginalText = text
		return strings.TrimSpace(text) != ""
	}
	var caption []string
	for _, line := range strings.Split(text, "\n") {
		if file, ok := omemo.ParseMediaURI(strings.TrimSpace(line)); ok {
			if len(in.EncryptedAttachments) < application.MaxMessageAttachments {
				in.EncryptedAttachments = append(in.EncryptedAttachments, file)
			}
		} else if !containsOMEMOMediaURI(line) {
			caption = append(caption, line)
		}
	}
	in.OriginalText = strings.Join(caption, "\n")
	return strings.TrimSpace(in.OriginalText) != "" || len(in.EncryptedAttachments) > 0
}

func containsOMEMOMediaURI(line string) bool {
	rest := strings.ToLower(line)
	for {
		_, suffix, ok := strings.Cut(rest, omemoMediaScheme)
		if !ok {
			return false
		}
		rest = suffix
		address := strings.TrimLeftFunc(suffix, unicode.IsSpace)
		if strings.HasPrefix(address, "/") {
			return true
		}
		if end := strings.IndexFunc(address, unicode.IsSpace); end >= 0 {
			address = address[:end]
		}
		if _, fragment, ok := strings.Cut(address, "#"); ok && fragment != "" {
			return true
		}
	}
}
