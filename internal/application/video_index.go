package application

import (
	"context"
	"net/url"
	"regexp"
)

const (
	VideoIndexMediaType = "application/vnd.oie.video-index+json"
	MaxVideoIndex       = 2 << 20
	MaxVideoFragment    = 32 << 20
)

type VideoIndex struct {
	Codecs     string         `json:"codecs"`
	Size       int64          `json:"size"`
	InitLength int64          `json:"initLength"`
	Segments   []VideoSegment `json:"segments"`
}

type VideoSegment struct {
	Duration float64 `json:"duration"`
	Length   int64   `json:"length"`
}

type VideoIndexReader interface {
	OpenVideoIndex(context.Context, string) (VideoIndex, error)
}

const peerTubeUUID = "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"

var peerTubeFragmentedPath = regexp.MustCompile(
	"^/(?:download/streaming-playlists/hls/videos|static/streaming-playlists/hls/(" +
		peerTubeUUID + "))/(" + peerTubeUUID + ")-[0-9]+-fragmented\\.mp4$",
)

// PeerTubeVideoID identifies only PeerTube's single-file HLS rendition paths.
func PeerTubeVideoID(address string) string {
	source, err := url.Parse(address)
	if err != nil || source.Scheme != "https" || source.Host == "" || source.User != nil ||
		source.RawPath != "" || source.RawQuery != "" || source.ForceQuery {
		return ""
	}
	match := peerTubeFragmentedPath.FindStringSubmatch(source.Path)
	if len(match) != 3 {
		return ""
	}
	if match[1] != "" {
		return match[1]
	}
	return match[2]
}
