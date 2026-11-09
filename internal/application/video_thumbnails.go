package application

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

// VideoThumbnailSource opens validated ranges from the current retained source.
// The decoder never receives a provider address or protocol credentials.
type VideoThumbnailSource func(context.Context, MediaReadRequest) (MediaRead, error)

type VideoThumbnailer interface {
	Available() bool
	Thumbnail(context.Context, VideoThumbnailSource) ([]byte, error)
}

// SuppliedThumbnailFetcher retrieves only a small advertised image, never a video.
type SuppliedThumbnailFetcher interface {
	SmallThumbnail(context.Context, string) ([]byte, error)
}

func normalizeMediaThumbnails(body string, hints []domain.MediaThumbnail) []domain.MediaThumbnail {
	var result []domain.MediaThumbnail
	seen := map[string]bool{}
	for _, hint := range hints {
		if len(result) >= 4 {
			break
		}
		if seen[hint.URL] || len(hint.ThumbnailURL) > 4096 {
			continue
		}
		u, err := url.Parse(hint.ThumbnailURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(hint.ThumbnailURL, "\r\n\t") {
			continue
		}
		for _, ref := range SharedReferences(body) {
			if ref.Kind == "video" && ref.URL == hint.URL {
				result = append(result, hint)
				seen[hint.URL] = true
				break
			}
		}
	}
	return result
}
func suppliedThumbnail(item domain.SharedContentItem) string {
	for _, hint := range normalizeMediaThumbnails(domain.DisplayBody(item.Message), item.Message.MediaThumbnails) {
		if hint.URL == item.URL {
			return hint.ThumbnailURL
		}
	}
	return ""
}
func (e *Engine) canGenerateThumbnail(item domain.SharedContentItem) bool {
	_, provided := e.previewFetcher.(SuppliedThumbnailFetcher)
	return e.mediaFiles != nil && (provided && suppliedThumbnail(item) != "" || e.videoThumbnailer != nil && e.videoThumbnailer.Available() && e.mediaReader != nil)
}

func (e *Engine) SetVideoThumbnailer(decoder VideoThumbnailer) { e.videoThumbnailer = decoder }

func (e *Engine) videoThumbnailItem(ctx context.Context, id domain.ID) (domain.SharedContentItem, error) {
	if !validID(id) {
		return domain.SharedContentItem{}, ErrInvalid
	}
	repo, ok := e.repo.(SharedMediaRepository)
	if !ok {
		return domain.SharedContentItem{}, ErrProtocolUnavailable
	}
	item, err := repo.SharedContentItem(ctx, id)
	if err != nil {
		return item, err
	}
	if item.ID != id || item.Kind != "video" {
		return item, ErrNotFound
	}
	source, err := e.Message(ctx, item.Message.ID)
	if err != nil {
		return item, err
	}
	if source.ID != item.Message.ID || source.AccountID != item.Message.AccountID || source.ConversationID != item.Message.ConversationID {
		return item, ErrNotFound
	}
	for _, ref := range SharedReferences(domain.DisplayBody(source)) {
		if ref.URL == item.URL && ref.Kind == "video" {
			item.Message = source
			return item, nil
		}
	}
	return item, ErrNotFound
}

func (e *Engine) ensureVideoPreview(ctx context.Context, repo PreviewRepository, item domain.SharedContentItem) (domain.MessagePreview, error) {
	id, err := e.newID()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	revision, err := e.newID()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	wanted := domain.MessagePreview{ID: id, RevisionID: revision, AccountID: item.Message.AccountID, ConversationID: item.Message.ConversationID, MessageID: item.Message.ID, SharedContentID: item.ID, URL: item.URL, Kind: "video", State: "not_requested", DownloadState: "not_downloaded", Title: item.FileName, PreviewVersion: previewRendererVersion, UpdatedAt: e.now().UTC()}
	p, err := repo.EnsurePreview(ctx, wanted)
	if err != nil {
		return p, err
	}
	if p.Kind != "video" || p.SharedContentID != item.ID {
		old := p.RevisionID
		wanted.ID = p.ID
		if err = repo.SavePreview(ctx, wanted, old); errors.Is(err, ErrStateConflict) {
			return e.VideoThumbnail(ctx, item.ID)
		}
		return wanted, err
	}
	return p, nil
}

// VideoThumbnail is a cache read. Ensuring metadata never queues network work.
func (e *Engine) VideoThumbnail(ctx context.Context, id domain.ID) (domain.MessagePreview, error) {
	item, err := e.videoThumbnailItem(ctx, id)
	if err != nil {
		return domain.MessagePreview{}, err
	}
	repo, err := e.previewRepo()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	return e.ensureVideoPreview(ctx, repo, item)
}

func (e *Engine) RequestVideoThumbnail(ctx context.Context, id domain.ID) (domain.MessagePreview, error) {
	for range 5 {
		p, err := e.VideoThumbnail(ctx, id)
		if err != nil {
			return p, err
		}
		if p.State == "ready" && p.ImageHash != "" || p.State == "queued" {
			return p, nil
		}
		item, err := e.videoThumbnailItem(ctx, id)
		if err != nil {
			return p, err
		}
		if !e.canGenerateThumbnail(item) {
			return p, ErrProtocolUnavailable
		}
		repo, err := e.previewRepo()
		if err != nil {
			return p, err
		}
		old := p.RevisionID
		p.RevisionID, err = e.newID()
		if err != nil {
			return p, err
		}
		p.State = "queued"
		p.LastErrorCode = ""
		p.UpdatedAt = e.now().UTC()
		if err = repo.SavePreview(ctx, p, old); errors.Is(err, ErrStateConflict) {
			continue
		} else if err != nil {
			return p, err
		}
		e.wakePreviews()
		return p, nil
	}
	return domain.MessagePreview{}, ErrStateConflict
}

func (e *Engine) processVideoThumbnail(ctx context.Context, repo PreviewRepository, p domain.MessagePreview) error {
	old := p.RevisionID
	job, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	item, err := e.videoThumbnailItem(job, p.SharedContentID)
	if err == nil && (item.Message.ID != p.MessageID || item.Message.AccountID != p.AccountID || item.Message.ConversationID != p.ConversationID || item.URL != p.URL) {
		err = ErrNotFound
	}
	var data []byte
	if err == nil {
		if provided, ok := e.previewFetcher.(SuppliedThumbnailFetcher); ok && suppliedThumbnail(item) != "" {
			data, _ = provided.SmallThumbnail(job, suppliedThumbnail(item))
		}
		// Invalid supplied images fall back to a bounded frame extraction too.
		if len(data) > 0 {
			if _, validation := e.mediaFiles.Thumbnail(job, data); validation != nil {
				data = nil
			}
		}
		if len(data) == 0 && (e.videoThumbnailer == nil || !e.videoThumbnailer.Available() || e.mediaReader == nil) {
			err = PreviewFailure{Code: "decoder_unavailable"}
		}
	}
	if err == nil && len(data) == 0 {
		data, err = e.videoThumbnailer.Thumbnail(job, func(readCtx context.Context, request MediaReadRequest) (MediaRead, error) {
			return e.SharedContentMedia(readCtx, p.SharedContentID, request)
		})
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		current, checkErr := e.videoThumbnailItem(job, p.SharedContentID)
		if checkErr != nil {
			err = checkErr
		} else if current.URL != p.URL || current.Message.ID != p.MessageID {
			err = ErrNotFound
		}
	}
	if err == nil {
		file, storeErr := e.mediaFiles.Thumbnail(job, data)
		err = storeErr
		if err == nil {
			err = repo.SaveMediaFile(job, file, e.now().UTC())
			if err == nil {
				p.ImageHash = file.Hash
			}
		}
	}
	if err != nil {
		p.State = "unavailable"
		p.LastErrorCode = "frame_unavailable"
		var failure PreviewFailure
		if errors.As(err, &failure) {
			p.LastErrorCode = failure.Code
		}
		if errors.Is(err, context.DeadlineExceeded) {
			p.LastErrorCode = "thumbnail_timeout"
		}
		if errors.Is(err, ErrNotFound) {
			p.LastErrorCode = "not_found"
		}
	} else {
		p.State = "ready"
		p.LastErrorCode = ""
	}
	var nextErr error
	p.RevisionID, nextErr = e.newID()
	if nextErr != nil {
		return nextErr
	}
	p.UpdatedAt = e.now().UTC()
	return repo.SavePreview(ctx, p, old)
}
