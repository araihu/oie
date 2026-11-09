package xmpp

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestFileThumbnailsDecodeNamespacesAndUnsupportedSources(t *testing.T) {
	raw := `<message xmlns='jabber:client'><body>https://files.example/clip.mp4</body><file-sharing xmlns='urn:xmpp:sfs:0'><file xmlns='urn:xmpp:file:metadata:0'><media-type>video/mp4</media-type><thumbnail xmlns='urn:xmpp:thumbs:1' uri='cid:ignored' media-type='image/png'/><thumbnail xmlns='urn:xmpp:thumbs:1' uri='https://files.example/poster.jpg' media-type='image/jpeg'/></file><sources><url-data xmlns='http://jabber.org/protocol/url-data' target='https://files.example/clip.mp4'/></sources></file-sharing></message>`
	var live struct {
		Body string `xml:"jabber:client body"`
		fileSharingMetadata
	}
	var archive archiveMessage
	if err := xml.Unmarshal([]byte(raw), &live); err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal([]byte(raw), &archive); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []fileSharingMetadata{live.fileSharingMetadata, archive.fileSharingMetadata} {
		refs := decodeFileThumbnails(meta)
		if len(refs) != 1 || refs[0].URL != "https://files.example/clip.mp4" || refs[0].ThumbnailURL != "https://files.example/poster.jpg" {
			t.Fatal(refs)
		}
	}
	for _, invalid := range []string{strings.ReplaceAll(raw, "urn:xmpp:sfs:0", "urn:wrong"), strings.ReplaceAll(raw, "urn:xmpp:thumbs:1", "urn:wrong"), strings.ReplaceAll(raw, "https://files.example/poster.jpg", "file:///etc/passwd"), strings.ReplaceAll(raw, "image/jpeg", "image/svg+xml")} {
		// Each untrusted stanza is decoded into a fresh value.
		var fresh archiveMessage
		if err := xml.Unmarshal([]byte(invalid), &fresh); err != nil {
			t.Fatal(err)
		}
		if refs := decodeFileThumbnails(fresh.fileSharingMetadata); len(refs) != 0 {
			t.Fatal(refs)
		}
	}
}
