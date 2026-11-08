package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func parseMediaRequest(params engineapi.GetSharedContentMediaParams) (application.MediaReadRequest, error) {
	out := application.MediaReadRequest{}
	if params.Range == nil {
		return out, nil
	}
	value := strings.TrimSpace(*params.Range)
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return out, application.ErrInvalid
	}
	fields := strings.TrimPrefix(value, "bytes=")
	for _, r := range fields {
		if r != '-' && (r < '0' || r > '9') {
			return out, application.ErrInvalid
		}
	}
	start, end, ok := strings.Cut(fields, "-")
	if !ok || end == "" && start == "" {
		return out, application.ErrInvalid
	}
	r := application.ByteRange{End: -1, Suffix: start == ""}
	var err error
	if r.Suffix {
		r.Start, err = strconv.ParseInt(end, 10, 64)
	} else {
		r.Start, err = strconv.ParseInt(start, 10, 64)
		if err == nil && end != "" {
			r.End, err = strconv.ParseInt(end, 10, 64)
		}
	}
	if err != nil || !r.Valid() {
		return out, application.ErrInvalid
	}
	out.Range = &r
	if params.IfRange != nil {
		condition := strings.TrimSpace(*params.IfRange)
		if validMediaTag(condition, true) {
			out.Condition.ETag = condition
		} else {
			at, err := http.ParseTime(condition)
			if err != nil {
				return out, application.ErrInvalid
			}
			out.Condition.ModifiedAt = at
		}
	}
	return out, nil
}
func validMediaTag(tag string, strong bool) bool {
	if strings.HasPrefix(tag, "W/") {
		if strong {
			return false
		}
		tag = strings.TrimPrefix(tag, "W/")
	}
	if len(tag) < 2 || len(tag) > 1024 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return false
	}
	for _, r := range tag[1 : len(tag)-1] {
		if r < 0x21 || r == '"' || r > 0x7e {
			return false
		}
	}
	return true
}

type sharedMediaResponse struct{ read application.MediaRead }

func (r sharedMediaResponse) VisitGetSharedContentMediaResponse(w http.ResponseWriter) error {
	defer func() { _ = r.read.Body.Close() }()
	w.Header().Set("Content-Type", r.read.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(r.read.Length, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, r.read.FileName)
	if name == "" {
		name = "video"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Accept-Ranges", "none")
	if r.read.RangeSupported {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	if validMediaTag(r.read.ETag, false) {
		w.Header().Set("ETag", r.read.ETag)
	}
	if !r.read.ModifiedAt.IsZero() {
		w.Header().Set("Last-Modified", r.read.ModifiedAt.UTC().Format(http.TimeFormat))
	}
	status := http.StatusOK
	if r.read.Partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", r.read.Offset, r.read.Offset+r.read.Length-1, r.read.Total))
	}
	controller := http.NewResponseController(w)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
	w.WriteHeader(status)
	if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	// A truncated/cancelled stream must end, never append a JSON problem body.
	_, _ = io.CopyN(mediaStreamWriter{writer: w, controller: controller}, r.read.Body, r.read.Length)
	return nil
}
func (s *Server) GetSharedContentMedia(ctx context.Context, r engineapi.GetSharedContentMediaRequestObject) (engineapi.GetSharedContentMediaResponseObject, error) {
	request, err := parseMediaRequest(r.Params)
	if err != nil {
		return nil, err
	}
	read, err := s.engine.SharedContentMedia(ctx, domain.ID(r.ItemId), request)
	if err != nil {
		return nil, err
	}
	return sharedMediaResponse{read: read}, nil
}

// Renew the deadline per bounded write, including flush, rather than timing the
// complete video. A stalled downstream cannot indefinitely retain a reader slot.
type mediaStreamWriter struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
}

func (w mediaStreamWriter) Write(p []byte) (int, error) {
	_ = w.controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
	n, err := w.writer.Write(p)
	if err == nil {
		if flush := w.controller.Flush(); flush != nil && !errors.Is(flush, http.ErrNotSupported) {
			err = flush
		}
	}
	return n, err
}
