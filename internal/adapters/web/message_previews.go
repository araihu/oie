package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) loadMessagePreviews(ctx context.Context, v *view) {
	if v.Selected == nil || v.Preview || len(v.Messages) == 0 {
		return
	}
	ids := make([]engineapi.ID, 0, len(v.Messages))
	visible := map[string]bool{}
	for _, m := range v.Messages {
		ids = append(ids, m.Id)
		visible[m.Id] = true
	}
	response, err := s.client.ListConversationPreviewsWithResponse(ctx, v.Selected.Id, &engineapi.ListConversationPreviewsParams{MessageId: &ids})
	if err != nil || response.JSON200 == nil {
		return
	}
	v.PreviewsKnown = true
	for _, p := range response.JSON200.Items {
		if p.ConversationId == v.Selected.Id && p.AccountId == v.Selected.AccountId && visible[p.MessageId] && safePreviewURL(p.Url) != "" {
			v.MessagePreviews[p.MessageId] = append(v.MessagePreviews[p.MessageId], p)
		}
	}
}

func safePreviewURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}
func previewHost(p engineapi.MessagePreview) string {
	u, err := url.Parse(safePreviewURL(p.Url))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}
func previewValue(value *string) string {
	if value != nil {
		return *value
	}
	return ""
}
func mediaSource(hash *string) string {
	if hash != nil && validAvatarHash(*hash) {
		return "/ui/media/" + *hash
	}
	return ""
}
func (v view) previewStatus(p engineapi.MessagePreview) string {
	if p.DownloadState == "queued" || p.DownloadState == "downloading" {
		return v.Text.LoadingImage
	}
	if p.DownloadState == "failed" {
		return v.previewFailure(previewValue(p.LastErrorCode))
	}
	if p.State == "queued" {
		return v.Text.PreviewLoading
	}
	if p.State == "unavailable" {
		return v.Text.PreviewUnavailable
	}
	return ""
}
func (v view) previewFailure(code string) string {
	switch code {
	case "blocked":
		return v.Text.ImageBlocked
	case "not_found":
		return v.Text.ImageNotFound
	case "too_large":
		return v.Text.ImageTooLarge
	case "unsupported":
		return v.Text.ImageUnsupported
	default:
		return v.Text.ImageDownloadFailed
	}
}

// mediaImage never retrieves a provider URL. Only locally retained, hash-verified
// PNG/JPEG/GIF data may cross this same-origin boundary into a browser image.
func (s *Server) mediaImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	hash := strings.TrimPrefix(r.URL.Path, "/ui/media/")
	if !validAvatarHash(hash) {
		w.WriteHeader(404)
		return
	}
	params := &engineapi.GetMediaImageParams{}
	if value := r.Header.Get("If-None-Match"); value != "" {
		params.IfNoneMatch = &value
	}
	response, err := s.client.GetMediaImage(r.Context(), hash, params)
	if err != nil {
		w.WriteHeader(503)
		return
	}
	defer func() { _ = response.Body.Close() }()
	cache := func() {
		w.Header().Set("ETag", `"`+hash+`"`)
	}
	if response.StatusCode == 304 && params.IfNoneMatch != nil {
		cache()
		w.WriteHeader(304)
		return
	}
	if response.StatusCode != 200 {
		if response.StatusCode == 404 {
			w.WriteHeader(404)
		} else {
			w.WriteHeader(503)
		}
		return
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/gif") {
		w.WriteHeader(502)
		return
	}
	const limit = 32 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(data) > limit {
		w.WriteHeader(502)
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	sum := sha256.Sum256(data)
	formats := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}
	if err != nil || config.Width < 1 || config.Height < 1 || formats[format] != contentType || hex.EncodeToString(sum[:]) != hash {
		w.WriteHeader(502)
		return
	}
	cache()
	w.Header().Set("Content-Type", contentType)
	if name := r.URL.Query().Get("download"); name != "" {
		name = path.Base(strings.ReplaceAll(name, "\\", "/"))
		name = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, name)
		if len(name) > 180 || name == "." || name == "/" || name == "" {
			name = "image"
		}
		extension := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}[contentType]
		current := strings.ToLower(path.Ext(name))
		if current != extension && (contentType != "image/jpeg" || current != ".jpeg") {
			name += extension
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	} else {
		w.Header().Set("Content-Disposition", `inline; filename="image"`)
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func previewFilename(p engineapi.MessagePreview) string {
	if p.Title != nil && *p.Title != "" {
		return *p.Title
	}
	u, err := url.Parse(safePreviewURL(p.Url))
	if err != nil {
		return ""
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" {
		return ""
	}
	return name
}
func previewMime(p engineapi.MessagePreview) string {
	if p.FileMimeType != nil {
		return string(*p.FileMimeType)
	}
	return ""
}
func previewSize(p engineapi.MessagePreview) string {
	if p.FileSize != nil {
		return strconv.FormatInt(*p.FileSize, 10)
	}
	return ""
}

// The destination retains its complete URL; only its display path is shortened.
func linkPreviewPath(raw string) string {
	u, err := url.Parse(safePreviewURL(raw))
	if err != nil || u == nil {
		return ""
	}
	value, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	if value == "/" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > 160 {
		return string(runes[:157]) + "..."
	}
	return value
}
func linkPreviewTitle(p engineapi.MessagePreview) string {
	title := strings.TrimSpace(previewValue(p.Title))
	host := previewHost(p)
	if title != "" && !strings.EqualFold(title, host) {
		return title
	}
	if value := linkPreviewPath(p.Url); value != "" {
		return value
	}
	return host
}
