package previewfetch

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/araihu/oie/internal/application"
)

func decimal(value string) (int64, error) {
	if value == "" {
		return 0, application.ErrInvalid
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, application.ErrInvalid
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err
}
func mediaFailure(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return application.MediaFailure{Code: "media_timeout"}
	}
	var failure application.PreviewFailure
	if errors.As(err, &failure) && failure.Code == "blocked" {
		return application.MediaFailure{Code: "media_blocked"}
	}
	return application.MediaFailure{Code: "media_fetch_failed"}
}
func rangeHeader(r application.ByteRange) string {
	if r.Suffix {
		return "bytes=-" + strconv.FormatInt(r.Start, 10)
	}
	end := ""
	if r.End >= 0 {
		end = strconv.FormatInt(r.End, 10)
	}
	return "bytes=" + strconv.FormatInt(r.Start, 10) + "-" + end
}
func safeEntityTag(tag string) string {
	if len(tag) > 1024 {
		return ""
	}
	raw := strings.TrimPrefix(tag, "W/")
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return ""
	}
	for _, r := range raw[1 : len(raw)-1] {
		if r < 0x21 || r > 0x7e || r == '"' {
			return ""
		}
	}
	return tag
}
func responseRange(value string) (start, end, total int64, err error) {
	bad := func() (int64, int64, int64, error) {
		return 0, 0, 0, application.MediaFailure{Code: "media_fetch_failed"}
	}
	if !strings.HasPrefix(value, "bytes ") {
		return bad()
	}
	span, size, ok := strings.Cut(strings.TrimPrefix(value, "bytes "), "/")
	if !ok {
		return bad()
	}
	from, to, ok := strings.Cut(span, "-")
	if !ok {
		return bad()
	}
	start, err = decimal(from)
	if err != nil {
		return bad()
	}
	end, err = decimal(to)
	if err != nil {
		return bad()
	}
	total, err = decimal(size)
	if err != nil || start > end || end >= total {
		return bad()
	}
	return start, end, total, nil
}
func rangeFailure(response *http.Response, request application.MediaReadRequest) error {
	value := response.Header.Get("Content-Range")
	if !strings.HasPrefix(value, "bytes */") {
		return application.MediaFailure{Code: "media_fetch_failed"}
	}
	total, err := decimal(strings.TrimPrefix(value, "bytes */"))
	if err != nil || request.Range == nil {
		return application.MediaFailure{Code: "media_fetch_failed"}
	}
	if total > request.Format.Limit() {
		return application.MediaFailure{Code: "media_too_large"}
	}
	if _, _, ok := request.Range.Resolve(total); ok {
		return application.MediaFailure{Code: "media_fetch_failed"}
	}
	return application.MediaFailure{Code: "range_not_satisfiable", Total: total}
}
func validateMediaResponse(response *http.Response, request application.MediaReadRequest) (application.MediaRead, error) {
	out := application.MediaRead{}
	switch response.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return out, application.ErrNotFound
	case http.StatusRequestedRangeNotSatisfiable:
		return out, rangeFailure(response, request)
	case http.StatusOK, http.StatusPartialContent:
	default:
		return out, application.MediaFailure{Code: "media_fetch_failed"}
	}
	encoding := strings.TrimSpace(response.Header.Get("Content-Encoding"))
	if encoding != "" && !strings.EqualFold(encoding, "identity") {
		return out, application.MediaFailure{Code: "media_fetch_failed"}
	}
	out.MediaType, _, _ = mime.ParseMediaType(response.Header.Get("Content-Type"))
	if request.Format.MediaType(out.MediaType) == "" {
		return out, application.MediaFailure{Code: "media_unsupported"}
	}
	out.Total = response.ContentLength
	out.Length = response.ContentLength
	out.Partial = response.StatusCode == http.StatusPartialContent
	if out.Partial {
		start, end, total, err := responseRange(response.Header.Get("Content-Range"))
		if err != nil {
			return out, err
		}
		out.Offset, out.Length, out.Total = start, end-start+1, total
		if request.Range == nil {
			return out, application.MediaFailure{Code: "media_fetch_failed"}
		}
		expectedStart, expectedEnd, ok := request.Range.Resolve(total)
		if !ok || start != expectedStart || end != expectedEnd || response.ContentLength >= 0 && response.ContentLength != out.Length {
			return out, application.MediaFailure{Code: "media_fetch_failed"}
		}
	} else if response.Header.Get("Content-Range") != "" {
		return out, application.MediaFailure{Code: "media_fetch_failed"}
	}
	if out.Total > request.Format.Limit() {
		return out, application.MediaFailure{Code: "media_too_large"}
	}
	if out.Total <= 0 || out.Length <= 0 {
		return out, application.MediaFailure{Code: "media_fetch_failed"}
	}
	out.MediaType = request.Format.MediaType(out.MediaType)
	out.RangeSupported = out.Partial || strings.EqualFold(response.Header.Get("Accept-Ranges"), "bytes")
	out.ETag = safeEntityTag(response.Header.Get("ETag"))
	out.ModifiedAt, _ = http.ParseTime(response.Header.Get("Last-Modified"))
	return out, nil
}

type mediaBody struct {
	io.ReadCloser
	remaining int64
	release   func()
	once      sync.Once
	closeErr  error
}

func (b *mediaBody) Close() error {
	b.once.Do(func() { b.closeErr = b.ReadCloser.Close(); b.release() })
	return b.closeErr
}
func (b *mediaBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:int(b.remaining)]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if errors.Is(err, io.EOF) && b.remaining > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// Open fetches only explicitly activated source bytes. It never writes the
// media store, creates thumbnails or represents partial bytes as a full hash.
func (f *Fetcher) Open(parent context.Context, address string, request application.MediaReadRequest) (application.MediaRead, error) {
	empty := application.MediaRead{}
	if request.Format.Limit() == 0 || request.Range != nil && !request.Range.Valid() {
		return empty, application.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	select {
	case f.mediaActive <- struct{}{}:
	default:
		cancel()
		return empty, application.MediaFailure{Code: "media_busy"}
	}
	var once sync.Once
	release := func() { once.Do(func() { cancel(); <-f.mediaActive }) }
	success := false
	defer func() {
		if !success {
			release()
		}
	}()
	u, err := url.Parse(address)
	if err != nil {
		return empty, application.MediaFailure{Code: "media_blocked"}
	}
	u = documentDownloadURL(u, request.Format)
	if _, err = f.destination(ctx, u); err != nil {
		return empty, mediaFailure(err)
	}
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return empty, application.MediaFailure{Code: "media_blocked"}
	}
	req.Header.Set("User-Agent", "Oie/0.15 media")
	req.Header.Set("Accept", "video/mp4, video/webm, video/ogg, video/quicktime, text/plain, text/markdown, text/csv, application/json, text/json, application/xml, text/xml, application/pdf")
	req.Header.Set("Accept-Encoding", "identity")
	if request.Range != nil {
		req.Header.Set("Range", rangeHeader(*request.Range))
		if request.Condition.ETag != "" {
			if safeEntityTag(request.Condition.ETag) != request.Condition.ETag || strings.HasPrefix(request.Condition.ETag, "W/") {
				return empty, application.ErrInvalid
			}
			req.Header.Set("If-Range", request.Condition.ETag)
		} else if !request.Condition.ModifiedAt.IsZero() {
			req.Header.Set("If-Range", request.Condition.ModifiedAt.UTC().Format(http.TimeFormat))
		}
	}
	response, err := f.mediaClient.Do(req)
	if err != nil {
		return empty, mediaFailure(err)
	}
	out, err := validateMediaResponse(response, request)
	if err != nil {
		_ = response.Body.Close()
		return empty, err
	}
	out.Body = &mediaBody{ReadCloser: response.Body, remaining: out.Length, release: release}
	success = true
	return out, nil
}

// Codeberg source links name repository documents but return an HTML page.
// Resolve the same host/path identity to the raw file before the normal public
// destination, MIME, size, redirect and byte-range checks run.
func documentDownloadURL(source *url.URL, format application.MediaFormat) *url.URL {
	if format != "md" || source.Scheme != "https" || source.Host != "codeberg.org" {
		return source
	}
	parts := strings.Split(source.Path, "/")
	if len(parts) < 7 || parts[1] == "" || parts[2] == "" || parts[3] != "src" || (parts[4] != "branch" && parts[4] != "tag" && parts[4] != "commit") {
		return source
	}
	// Avoid interpreting escaped separators as a different repository identity.
	if source.RawPath != "" {
		return source
	}
	result := *source
	parts[3] = "raw"
	result.Path = strings.Join(parts, "/")
	return &result
}
