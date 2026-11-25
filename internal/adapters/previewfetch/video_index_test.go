package previewfetch

import (
	"fmt"
	"strings"
	"testing"
)

func TestPeerTubeIndexMatchesOneContiguousOriginal(t *testing.T) {
	const file = "clip-fragmented.mp4"
	const playlist = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-PLAYLIST-TYPE:VOD
#EXT-X-MAP:URI="clip-fragmented.mp4",BYTERANGE="12@0"
#EXTINF:4.000000,
#EXT-X-BYTERANGE:50@12
clip-fragmented.mp4
#EXTINF:1.033333,
#EXT-X-BYTERANGE:8@62
clip-fragmented.mp4
#EXT-X-ENDLIST`
	const master = `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1200000,CODECS="avc1.640028,mp4a.40.2",RESOLUTION=1920x1080
clip.m3u8`
	codecs := peerTubeVariantCodecs(master, "clip.m3u8")
	index, ok := parsePeerTubeIndex(playlist, file, codecs, 70)
	if !ok || codecs != "avc1.640028,mp4a.40.2" || index.InitLength != 12 || index.Size != 70 ||
		len(index.Segments) != 2 || index.Segments[0].Length != 50 || index.Segments[1].Duration != 1.033333 {
		t.Fatal(index, ok)
	}
	if peerTubeVariantCodecs(master, "another.m3u8") != "" {
		t.Fatal("accepted codecs for a different rendition")
	}
	for _, tc := range []struct{ name, from, to string }{
		{"gap", "50@12", "50@13"},
		{"overlap", "8@62", "8@61"},
		{"wrong total", "8@62", "7@62"},
		{"malformed duration", "1.033333", "NaN"},
		{"different initialization", `URI="clip-fragmented.mp4"`, `URI="other.mp4"`},
		{"remote segment", "\n" + file + "\n", "\nhttps://elsewhere.example/" + file + "\n"},
		{"encrypted", "#EXT-X-VERSION:7", "#EXT-X-KEY:METHOD=AES-128"},
		{"live", "#EXT-X-ENDLIST", ""},
		{"trailing bytes", "#EXT-X-ENDLIST", fmt.Sprintf("#EXT-X-ENDLIST\n%s", file)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := parsePeerTubeIndex(strings.Replace(playlist, tc.from, tc.to, 1), file, codecs, 70); ok {
				t.Fatal("accepted an invalid index")
			}
		})
	}
}
