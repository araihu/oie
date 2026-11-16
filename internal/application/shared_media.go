package application

import (
	"context"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

const (
	MaxMediaSource int64 = 2 << 30
	MaxAudioSource int64 = 100 << 20
	MaxTextSource  int64 = 2 << 20
	MaxPDFSource   int64 = 100 << 20
)

// MediaFormat is selected from a retained canonical reference, never a client URL.
// Its zero value preserves the original video reader contract.
type MediaFormat string

func (f MediaFormat) Limit() int64 {
	switch f {
	case "", "video":
		return MaxMediaSource
	case "audio":
		return MaxAudioSource
	case "txt", "log", "md", "csv", "json", "xml":
		return MaxTextSource
	case "pdf":
		return MaxPDFSource
	}
	return 0
}

// MediaType validates provider MIME against the source format and returns the safe
// response MIME. Structured textual documents are deliberately served as plain text.
func (f MediaFormat) MediaType(value string) string {
	if f == "" || f == "video" {
		if VideoMediaType(value) {
			return value
		}
		return ""
	}
	if f == "audio" {
		return domain.PlayableAudioType(value)
	}
	if f == "pdf" {
		if value == "application/pdf" {
			return value
		}
		return ""
	}
	if f.Limit() == 0 {
		return ""
	}
	if value == "text/plain" {
		return "text/plain"
	}
	switch f {
	case "md":
		if value == "text/markdown" {
			return "text/plain"
		}
	case "csv":
		if value == "text/csv" {
			return "text/plain"
		}
	case "json":
		if value == "application/json" || value == "text/json" {
			return "text/plain"
		}
	case "xml":
		if value == "application/xml" || value == "text/xml" {
			return "text/plain"
		}
	}
	return ""
}
func sharedMediaFormat(item domain.SharedContentItem) (MediaFormat, bool) {
	if item.Kind == "video" {
		return "video", true
	}
	if item.Kind == "audio" {
		return "audio", true
	}
	if item.Kind != "document" {
		return "", false
	}
	u, err := url.Parse(item.URL)
	if err != nil {
		return "", false
	}
	format := MediaFormat(strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), "."))
	if format.Limit() == 0 {
		format = MediaFormat(strings.TrimPrefix(strings.ToLower(path.Ext(item.FileName)), "."))
	}
	return format, format.Limit() > 0 && format != "video" && format != "audio"
}

// ByteRange is an inclusive byte selection. End=-1 means open-ended; for a
// suffix selection Start is the positive suffix length and End remains -1.
type ByteRange struct {
	Start, End int64
	Suffix     bool
}

func (r ByteRange) Valid() bool {
	return r.Start >= 0 && r.End >= -1 && ((!r.Suffix && (r.End == -1 || r.End >= r.Start)) || (r.Suffix && r.Start > 0 && r.End == -1))
}
func (r ByteRange) Resolve(total int64) (start, end int64, ok bool) {
	if !r.Valid() || total <= 0 {
		return 0, 0, false
	}
	if r.Suffix {
		start = total - r.Start
		if start < 0 {
			start = 0
		}
		return start, total - 1, true
	}
	if r.Start >= total {
		return 0, 0, false
	}
	end = r.End
	if end < 0 || end >= total {
		end = total - 1
	}
	return r.Start, end, true
}

type MediaCondition struct {
	ETag       string
	ModifiedAt time.Time
}
type MediaReadRequest struct {
	Format    MediaFormat
	Download  bool
	DeviceID  domain.ID
	Range     *ByteRange
	Condition MediaCondition
}
type MediaRead struct {
	FileName                  string
	SHA256, VerifiedAlgorithm string
	Body                      io.ReadCloser
	MediaType                 string
	Length, Total, Offset     int64
	Partial, RangeSupported   bool
	ETag                      string
	ModifiedAt                time.Time
}
type MediaReader interface {
	Open(context.Context, string, MediaReadRequest) (MediaRead, error)
}
type SharedMediaRepository interface {
	SharedContentItem(context.Context, domain.ID) (domain.SharedContentItem, error)
}
type MediaFailure struct {
	Code  string
	Total int64
}

func (e MediaFailure) Error() string                { return e.Code }
func (e *Engine) SetMediaReader(reader MediaReader) { e.mediaReader = reader }
func VideoMediaType(value string) bool {
	return value == "video/mp4" || value == "video/webm" || value == "video/ogg" || value == "video/quicktime"
}

// SharedContentMedia opens only a current retained reference after explicit
// activation. Streamed ranges do not mutate downloads, hashes, messages or stars.
func (e *Engine) SharedContentMedia(ctx context.Context, id domain.ID, request MediaReadRequest) (MediaRead, error) {
	if !validID(id) || request.DeviceID != "" && (!request.Download || !validID(request.DeviceID)) || (request.Range != nil && !request.Range.Valid()) || (request.Condition.ETag != "" && !request.Condition.ModifiedAt.IsZero()) {
		return MediaRead{}, ErrInvalid
	}
	repo, ok := e.repo.(SharedMediaRepository)
	if !ok || e.mediaReader == nil {
		return MediaRead{}, ErrProtocolUnavailable
	}
	item, err := repo.SharedContentItem(ctx, id)
	if err != nil {
		return MediaRead{}, err
	}
	format, supported := sharedMediaFormat(item)
	if item.ID != id || !supported {
		return MediaRead{}, ErrNotFound
	}
	source, err := e.Message(ctx, item.Message.ID)
	if err != nil {
		return MediaRead{}, err
	}
	if source.ID != item.Message.ID || source.AccountID != item.Message.AccountID || source.ConversationID != item.Message.ConversationID {
		return MediaRead{}, ErrNotFound
	}
	current := false
	for _, ref := range MessageSharedReferences(source) {
		if ref.URL == item.URL && ref.Kind == item.Kind {
			current = true
			break
		}
	}
	if !current {
		return MediaRead{}, ErrNotFound
	}
	expected := fileDigests(source, item.URL)
	var generation int64
	if request.DeviceID != "" {
		repo, ok := e.repo.(DownloadRepository)
		if !ok || e.downloadFiles == nil {
			return MediaRead{}, ErrProtocolUnavailable
		}
		retained, current, err := e.reuseDownload(ctx, repo, request.DeviceID, item, source)
		if err == nil {
			return retained, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return MediaRead{}, err
		}
		generation = current
	}
	if request.Download {
		request.Range, request.Condition = nil, MediaCondition{}
	}
	request.Format = format
	result, err := e.mediaReader.Open(ctx, item.URL, request)
	if err != nil {
		return MediaRead{}, err
	}
	valid := result.Body != nil && format.MediaType(result.MediaType) != "" && result.Length > 0 && result.Total > 0 && result.Total <= format.Limit() && result.Length <= result.Total && result.Offset >= 0
	if result.Partial {
		valid = valid && request.Range != nil
		if request.Range != nil {
			start, end, ok := request.Range.Resolve(result.Total)
			valid = valid && ok && result.Offset == start && result.Length == end-start+1
		}
	} else {
		valid = valid && result.Offset == 0 && result.Length == result.Total
	}
	if !valid {
		if result.Body != nil {
			_ = result.Body.Close()
		}
		return MediaRead{}, MediaFailure{Code: "media_fetch_failed"}
	}
	result.MediaType = format.MediaType(result.MediaType)
	result.FileName = item.FileName
	if request.Download || request.Range == nil && len(expected) > 0 {
		ref := domain.Download{DeviceID: request.DeviceID, SourceID: item.ID, SourceRevision: source.RevisionID, Kind: "file"}
		return e.completeDownload(ctx, result, expected, ref, generation)
	}
	return result, nil
}
