package web

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// A media source is a retained catalog identity, never a client-supplied URL.
func (s *Server) sharedMediaContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/ui/shared-content/"), "/content")
	if !strings.HasSuffix(r.URL.Path, "/content") || !validOperationKey(id) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	query := r.URL.Query()
	for key, values := range query {
		if key != "engineId" && key != "download" || len(values) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	if query.Has("download") && query.Get("download") != "1" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if query.Has("engineId") {
		if !validOperationKey(query.Get("engineId")) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		engine, err := s.client.GetEngineWithResponse(r.Context())
		if err != nil || engine.JSON200 == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if engine.JSON200.Id != query.Get("engineId") {
			w.WriteHeader(http.StatusConflict)
			return
		}
	}
	params := &engineapi.GetSharedContentMediaParams{}
	if value := r.Header.Get("Range"); value != "" {
		if len(value) > 128 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		params.Range = &value
	}
	if value := r.Header.Get("If-Range"); value != "" {
		if len(value) > 1024 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		params.IfRange = &value
	}
	response, err := s.streamClient.GetSharedContentMedia(r.Context(), id, params)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		switch response.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusUnsupportedMediaType,
			http.StatusRequestedRangeNotSatisfiable, http.StatusBadGateway, http.StatusServiceUnavailable:
			body, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
			if readErr != nil || len(body) > 16<<10 || len(body) > 0 && !json.Valid(body) {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
				if value := response.Header.Get("Content-Range"); validUnsatisfiedVideoRange(value) {
					w.Header().Set("Content-Range", value)
				}
			}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(response.StatusCode)
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
		return
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	maximum := sharedMediaMaximum(mediaType)
	if err != nil || maximum == 0 || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if response.ContentLength < 0 || response.ContentLength > maximum {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if response.StatusCode == http.StatusPartialContent && !validSatisfiedVideoRange(response.Header.Get("Content-Range"), response.ContentLength, maximum) {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	for _, key := range []string{"Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if value := response.Header.Get(key); len(value) <= 1024 && value != "" {
			w.Header().Set(key, value)
		}
	}
	filename := "video"
	switch mediaType {
	case "application/pdf":
		filename = "document.pdf"
	case "text/plain":
		filename = "document.txt"
	}
	if _, attributes, parseErr := mime.ParseMediaType(response.Header.Get("Content-Disposition")); parseErr == nil {
		candidate := path.Base(strings.ReplaceAll(attributes["filename"], "\\", "/"))
		if candidate != "" && candidate != "." && candidate != "/" && len(candidate) <= 300 && !strings.ContainsAny(candidate, "\r\n\x00") {
			filename = candidate
		}
	}
	disposition := "inline"
	if query.Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filename}))
	control := http.NewResponseController(w)
	_ = control.SetWriteDeadline(time.Now().Add(15 * time.Second))
	defer func() { _ = control.SetWriteDeadline(time.Time{}) }()
	w.WriteHeader(response.StatusCode)
	if control.Flush() != nil {
		return
	}
	buffer := make([]byte, 32<<10)
	remaining := response.ContentLength
	for remaining > 0 {
		n, readErr := response.Body.Read(buffer[:min(int64(len(buffer)), remaining)])
		if n > 0 {
			_ = control.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			remaining -= int64(n)
			if control.Flush() != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}

func sharedMediaMaximum(kind string) int64 {
	if videoMediaType(kind) {
		return 2 << 30
	}
	switch kind {
	case "text/plain":
		return 2 << 20
	case "application/pdf":
		return 100 << 20
	default:
		return 0
	}
}

func videoMediaType(value string) bool {
	return value == "video/mp4" || value == "video/webm" || value == "video/ogg" || value == "video/quicktime"
}

func validUnsatisfiedVideoRange(value string) bool {
	if !strings.HasPrefix(value, "bytes */") {
		return false
	}
	total, err := strconv.ParseUint(strings.TrimPrefix(value, "bytes */"), 10, 63)
	return err == nil && total <= 2<<30
}

func validSatisfiedVideoRange(value string, length, maximum int64) bool {
	if !strings.HasPrefix(value, "bytes ") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes "), "/")
	if len(parts) != 2 {
		return false
	}
	bounds := strings.Split(parts[0], "-")
	if len(bounds) != 2 {
		return false
	}
	start, e1 := strconv.ParseInt(bounds[0], 10, 64)
	end, e2 := strconv.ParseInt(bounds[1], 10, 64)
	total, e3 := strconv.ParseInt(parts[1], 10, 64)
	return e1 == nil && e2 == nil && e3 == nil && start >= 0 && end >= start && total > end && total <= maximum && end-start+1 == length
}

func (v view) messageVideos(id string) []engineapi.SharedContentItem { return v.MessageVideos[id] }

var messageBodyURLs = regexp.MustCompile(`https?://[^\s<>"\x00-\x1f]+`)

func hasVideoReference(body, address string) bool {
	for _, candidate := range messageBodyURLs.FindAllString(body, 128) {
		if strings.TrimRight(candidate, ".,;!)]}") == address {
			return true
		}
	}
	return false
}

func (s *Server) loadMessageVideos(ctx context.Context, v *view) {
	if v.Selected == nil || v.Preview || len(v.Messages) == 0 {
		return
	}
	ids := make([]engineapi.ID, 0, len(v.Messages))
	visible := make(map[string]messageView, len(v.Messages))
	for _, message := range v.Messages {
		if len(ids) == 100 {
			break
		}
		ids = append(ids, message.Id)
		visible[message.Id] = message
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	limit, kind := 100, engineapi.ListSharedContentParamsType("video")
	params := &engineapi.ListSharedContentParams{Type: &kind, Limit: &limit, ConversationId: &v.Selected.Id, AccountId: &v.Selected.AccountId, MessageId: &ids}
	items := map[string][]engineapi.SharedContentItem{}
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		response, err := s.client.ListSharedContentWithResponse(ctx, params)
		if err != nil || response.JSON200 == nil {
			return
		}
		for _, item := range response.JSON200.Items {
			message, found := visible[item.Message.Id]
			if !found || item.Kind != "video" || item.Message.AccountId != v.Selected.AccountId || item.Message.ConversationId != v.Selected.Id || item.Message.RevisionId != message.RevisionId || item.Message.OriginalText != message.OriginalText || !validOperationKey(item.Id) || safePreviewURL(item.Url) == "" || !hasVideoReference(message.displayBody(), item.Url) {
				return
			}
			if !seen[item.Id] && len(items[item.Message.Id]) < 3 {
				items[item.Message.Id] = append(items[item.Message.Id], item)
				seen[item.Id] = true
			}
		}
		if response.JSON200.NextCursor == nil || params.Cursor != nil && *params.Cursor == *response.JSON200.NextCursor {
			break
		}
		params.Cursor = response.JSON200.NextCursor
	}
	v.MessageVideos = items
	for id, videos := range items {
		urls := map[string]bool{}
		for _, video := range videos {
			urls[video.Url] = true
		}
		previews := v.MessagePreviews[id]
		kept := make([]engineapi.MessagePreview, 0, len(previews))
		for _, preview := range previews {
			if !urls[preview.Url] {
				kept = append(kept, preview)
			}
		}
		if v.MessagePreviews != nil {
			v.MessagePreviews[id] = kept
		}
	}
}

func videoPresentation(original string, items []engineapi.SharedContentItem) string {
	urls := make(map[string]bool, len(items))
	for _, item := range items {
		urls[item.Url] = true
	}
	return referencePresentation(original, urls)
}
