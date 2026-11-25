package xmpp

import (
	"strings"
	"testing"

	"github.com/araihu/oie/internal/application"
)

func TestOMEMOPlainTextCanNameTheAttachmentScheme(t *testing.T) {
	const body = "O esquema aesgcm: é para anexos.\nDocumente aesgcm: no ticket #42."
	var in application.Incoming
	if !readOMEMOPayload(&in, body) || in.OriginalText != body || len(in.EncryptedAttachments) != 0 {
		t.Fatalf("ordinary text was treated as an attachment: %+v", in)
	}
}

func TestOMEMOMalformedMediaReferencesStayOutOfMessageText(t *testing.T) {
	for _, line := range []string{
		"invalid aesgcm://upload.example.org/file#example-key",
		"AESGCM:upload.example.org/file#example-key",
		"aesgcm: https://upload.example.org/file#example-key",
		"aesgcm:/file#example-key",
	} {
		var in application.Incoming
		if !readOMEMOPayload(&in, "caption\n"+line) || in.OriginalText != "caption" || len(in.EncryptedAttachments) != 0 {
			t.Fatalf("malformed reference reached message text: %+v", in)
		}
	}
	var in application.Incoming
	body := "caption\naesgcm://upload.example.org/file#" + strings.Repeat("ab", 44)
	if !readOMEMOPayload(&in, body) || in.OriginalText != "caption" || len(in.EncryptedAttachments) != 1 {
		t.Fatalf("valid attachment was not extracted: %+v", in)
	}
}
