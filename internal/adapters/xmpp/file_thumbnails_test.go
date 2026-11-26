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

func TestAdvertisedOriginalDigestsUseOnlyFileMetadataHashes(t *testing.T) {
	raw := `<message xmlns='jabber:client'>
		<file-sharing xmlns='urn:xmpp:sfs:0'><file xmlns='urn:xmpp:file:metadata:0'>
			<media-type>audio/mpeg</media-type><name>meeting recording.mp3</name>
			<hash xmlns='urn:xmpp:hashes:2' algo='sha-256'>2XarmwTlNxDAMkvymloX3S5+VbylNrJt/l5QyPa+YoU=</hash>
			<hash xmlns='urn:xmpp:hashes:2' algo='sha-512'>invalid</hash>
			<hash xmlns='urn:xmpp:hashes:2' algo='sha-1'>2AfMGH8O7UNPTvUVAM9aK13mpCY=</hash>
			<thumbnail xmlns='urn:xmpp:thumbs:1'><hash xmlns='urn:xmpp:hashes:2' algo='sha-256'>AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=</hash></thumbnail>
		</file><sources><url-data xmlns='http://jabber.org/protocol/url-data' target='https://files.example/voice'/></sources></file-sharing>
	</message>`
	var archive archiveMessage
	if err := xml.Unmarshal([]byte(raw), &archive); err != nil {
		t.Fatal(err)
	}
	meta := archive.fileSharingMetadata
	digests := decodeFileDigests(meta)
	if len(digests) != 1 || digests[0].URL != "https://files.example/voice" || !digests[0].Valid() || digests[0].Algorithm != "sha-256" {
		t.Fatal(digests)
	}
	urls := decodeAudioURLs(meta)
	if len(urls) != 1 || urls[0] != digests[0].URL || attachmentBodyFallback("", meta) != urls[0] {
		t.Fatal(urls)
	}
	refs := decodeFileReferences(meta)
	if len(refs) != 1 || refs[0].Kind != "audio" || refs[0].URL != urls[0] || refs[0].FileName != "meeting recording.mp3" {
		t.Fatal(refs)
	}
	if attachmentBodyFallback("caption", meta) != "caption" {
		t.Fatal("an explicit wire caption was changed")
	}
}

func TestStickerRequiresDirectNamespacedMarkerAndSharedImage(t *testing.T) {
	const marker = "<sticker xmlns='urn:xmpp:stickers:0'/>"
	const source = "https://files.example/sticker.gif"
	for _, tc := range []struct {
		name, marker, mediaType string
		want                    bool
	}{
		{"standalone", marker, "image/gif", true},
		{"pack", "<sticker xmlns='urn:xmpp:stickers:0' pack='abc'/>", "image/png", true},
		{"ordinary", "", "image/gif", false},
		{"wrong namespace", "<sticker xmlns='urn:other'/>", "image/gif", false},
		{"nested", "<other>" + marker + "</other>", "image/gif", false},
		{"audio", marker, "audio/mpeg", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "<message xmlns='jabber:client'>" + tc.marker + "<file-sharing xmlns='urn:xmpp:sfs:0'><file xmlns='urn:xmpp:file:metadata:0'><media-type>" + tc.mediaType + "</media-type></file><sources><url-data xmlns='http://jabber.org/protocol/url-data' target='" + source + "'/></sources></file-sharing></message>"
			var msg archiveMessage
			if err := xml.Unmarshal([]byte(raw), &msg); err != nil {
				t.Fatal(err)
			}
			refs := decodeFileReferences(msg.fileSharingMetadata)
			if len(refs) != 1 || refs[0].Sticker != tc.want || refs[0].URL != source {
				t.Fatalf("references: %+v", refs)
			}
		})
	}
}
