package previewfetch

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
)

var (
	codecsAttribute = regexp.MustCompile(`(?:^|,)CODECS="([A-Za-z0-9.,-]{1,128})"(?:,|$)`)
	indexMap        = regexp.MustCompile(`^#EXT-X-MAP:URI="([^"]+)",BYTERANGE="([0-9]+)@0"$`)
)

func (f *Fetcher) OpenVideoIndex(parent context.Context, address string) (application.VideoIndex, error) {
	unavailable := application.MediaFailure{Code: "media_unsupported"}
	var empty application.VideoIndex
	id := application.PeerTubeVideoID(address)
	if id == "" {
		return empty, unavailable
	}
	select {
	case f.mediaActive <- struct{}{}:
		defer func() { <-f.mediaActive }()
	default:
		return empty, application.MediaFailure{Code: "media_busy"}
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	source, _ := url.Parse(address)
	source.Fragment = ""
	api := source.ResolveReference(&url.URL{Path: "/api/v1/videos/" + id})
	body, err := f.readIndexResource(ctx, api, "application/json")
	if err != nil {
		return empty, err
	}
	var metadata struct {
		StreamingPlaylists []struct {
			PlaylistURL string
			Files       []struct {
				FileURL, FileDownloadURL string
				Size                     int64
			}
		}
	}
	if json.Unmarshal(body, &metadata) != nil {
		return empty, unavailable
	}
	for _, playlist := range metadata.StreamingPlaylists {
		for _, file := range playlist.Files {
			if file.FileURL != source.String() && file.FileDownloadURL != source.String() {
				continue
			}
			if file.Size <= 0 || file.Size > application.MaxMediaSource {
				return empty, unavailable
			}
			master := peerTubeIndexURL(source, playlist.PlaylistURL, id, ".m3u8")
			static := peerTubeIndexURL(source, file.FileURL, id, "-fragmented.mp4")
			if master == nil || static == nil {
				return empty, unavailable
			}
			variant := strings.TrimSuffix(path.Base(static.Path), "-fragmented.mp4") + ".m3u8"
			body, err := f.readIndexResource(ctx, master, "application/vnd.apple.mpegurl")
			if err != nil {
				return empty, err
			}
			codecs := peerTubeVariantCodecs(string(body), variant)
			if codecs == "" {
				return empty, unavailable
			}
			body, err = f.readIndexResource(ctx, master.ResolveReference(&url.URL{Path: variant}), "application/vnd.apple.mpegurl")
			if err != nil {
				return empty, err
			}
			if index, ok := parsePeerTubeIndex(string(body), path.Base(static.Path), codecs, file.Size); ok {
				return index, nil
			}
			return empty, unavailable
		}
	}
	return empty, unavailable
}

func peerTubeIndexURL(source *url.URL, address, id, suffix string) *url.URL {
	target, err := url.Parse(address)
	if err != nil || target.Scheme != source.Scheme || target.Host != source.Host || target.User != nil ||
		target.RawPath != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" ||
		path.Dir(target.Path) != "/static/streaming-playlists/hls/"+id || !strings.HasSuffix(path.Base(target.Path), suffix) {
		return nil
	}
	return target
}

func (f *Fetcher) readIndexResource(ctx context.Context, address *url.URL, accept string) ([]byte, error) {
	if _, err := f.destination(ctx, address); err != nil {
		return nil, mediaFailure(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return nil, mediaFailure(err)
	}
	request.Header.Set("Accept", accept)
	client := *f.client
	// Metadata may describe other hosts. Only same-origin, canonical resources
	// for this exact rendition participate in indexed playback.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, mediaFailure(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, application.MediaFailure{Code: "media_unsupported"}
	}
	body, err := readBounded(response, application.MaxVideoIndex)
	if err != nil {
		return nil, mediaFailure(err)
	}
	return body, nil
}

func peerTubeVariantCodecs(manifest, variant string) string {
	lines := strings.Split(strings.TrimSpace(manifest), "\n")
	if strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		info, ok := strings.CutPrefix(strings.TrimSpace(lines[i-1]), "#EXT-X-STREAM-INF:")
		if strings.TrimSpace(lines[i]) == variant && ok {
			if match := codecsAttribute.FindStringSubmatch(info); match != nil {
				return match[1]
			}
		}
	}
	return ""
}

func parsePeerTubeIndex(manifest, filename, codecs string, size int64) (application.VideoIndex, bool) {
	lines := strings.Split(strings.TrimSpace(manifest), "\n")
	var index application.VideoIndex
	if strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return index, false
	}
	var end, length int64
	var duration float64
	complete := false
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		switch {
		case complete:
			return index, false
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			match := indexMap.FindStringSubmatch(line)
			if match == nil || match[1] != filename || end != 0 {
				return index, false
			}
			end, _ = strconv.ParseInt(match[2], 10, 64)
			if end <= 0 || end > application.MaxVideoFragment || end >= size {
				return index, false
			}
			index.InitLength = end
		case strings.HasPrefix(line, "#EXTINF:"):
			if end == 0 || duration != 0 {
				return index, false
			}
			value, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			duration, _ = strconv.ParseFloat(value, 64)
			if math.IsNaN(duration) || duration <= 0 || duration > 60 {
				return index, false
			}
		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			count, offset, ok := strings.Cut(strings.TrimPrefix(line, "#EXT-X-BYTERANGE:"), "@")
			n, countErr := strconv.ParseInt(count, 10, 64)
			start, offsetErr := strconv.ParseInt(offset, 10, 64)
			if !ok || countErr != nil || offsetErr != nil || duration == 0 || length != 0 ||
				start != end || n <= 0 || n > application.MaxVideoFragment || n > size-end {
				return index, false
			}
			length = n
		case line == filename:
			if length == 0 || len(index.Segments) >= 16000 {
				return index, false
			}
			index.Segments = append(index.Segments, application.VideoSegment{Duration: duration, Length: length})
			end += length
			length, duration = 0, 0
		case line == "#EXT-X-ENDLIST":
			complete = true
		case line == "" || strings.HasPrefix(line, "#EXT-X-VERSION:") ||
			strings.HasPrefix(line, "#EXT-X-TARGETDURATION:") ||
			strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:") ||
			line == "#EXT-X-PLAYLIST-TYPE:VOD" || line == "#EXT-X-INDEPENDENT-SEGMENTS":
		default:
			return index, false
		}
	}
	index.Codecs, index.Size = codecs, size
	return index, complete && end == size && length == 0 && duration == 0 && len(index.Segments) > 0
}
