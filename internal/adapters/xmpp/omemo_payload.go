package xmpp

import (
	"strings"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
)

func readOMEMOPayload(in *application.Incoming, text string) bool {
	if !strings.Contains(strings.ToLower(text), "aesgcm:") {
		in.OriginalText = text
		return strings.TrimSpace(text) != ""
	}
	var caption []string
	for _, line := range strings.Split(text, "\n") {
		if file, ok := omemo.ParseMediaURI(strings.TrimSpace(line)); ok {
			if len(in.EncryptedAttachments) < application.MaxMessageAttachments {
				in.EncryptedAttachments = append(in.EncryptedAttachments, file)
			}
		} else if !strings.Contains(strings.ToLower(line), "aesgcm:") {
			caption = append(caption, line)
		}
	}
	in.OriginalText = strings.Join(caption, "\n")
	return strings.TrimSpace(in.OriginalText) != "" || len(in.EncryptedAttachments) > 0
}
