package application

import (
	"context"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

// PreviewRepository owns derived cache records, never message/outbox transitions.
type PreviewRepository interface {
	Preview(context.Context, domain.ID) (domain.MessagePreview, error)
	EnsurePreview(context.Context, domain.MessagePreview) (domain.MessagePreview, error)
	SavePreview(context.Context, domain.MessagePreview, domain.ID) error
	PreviewJobs(context.Context, time.Time, int) ([]domain.MessagePreview, error)
	InterruptedPreviewJobs(context.Context, int) ([]domain.MessagePreview, error)
	SaveMediaFile(context.Context, domain.MediaFile, time.Time) error
	MediaFile(context.Context, string) (domain.MediaFile, error)
}
type PreviewMetadata struct {
	Title, Description, SiteName string
	Image, Favicon               []byte
	IsImage                      bool
}
type PreviewFetcher interface {
	Link(context.Context, string) (PreviewMetadata, error)
	Image(context.Context, string) ([]byte, error)
}
type PreviewCacheInvalidator interface {
	InvalidateLink(string)
}

type MediaFiles interface {
	Put(context.Context, []byte, bool) (domain.MediaFile, error)
	Thumbnail(context.Context, []byte) (domain.MediaFile, error)
	Read(context.Context, domain.MediaFile) ([]byte, error)
}
type PreviewFailure struct{ Code string }

func (f PreviewFailure) Error() string { return f.Code }
func (e *Engine) SetPreviewServices(fetcher PreviewFetcher, files MediaFiles) {
	e.previewFetcher = fetcher
	e.mediaFiles = files
}
func (e *Engine) previewRepo() (PreviewRepository, error) {
	r, ok := e.repo.(PreviewRepository)
	if !ok {
		return nil, ErrProtocolUnavailable
	}
	return r, nil
}

const previewRendererVersion = 3

var messageURLs = regexp.MustCompile(`https?://[^\s<>"\x00-\x1f]+`)

func previewURLs(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, candidate := range messageURLs.FindAllString(text, 12) {
		candidate = strings.TrimRight(candidate, ".,;!)]}")
		if len(candidate) > 4096 || seen[candidate] {
			continue
		}
		u, err := url.Parse(candidate)
		if err != nil || u.Hostname() == "" || u.User != nil {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
		if len(out) == 3 {
			break
		}
	}
	return out
}
func (e *Engine) ConversationPreviews(ctx context.Context, conversation domain.ID, ids []domain.ID) ([]domain.MessagePreview, error) {
	c, err := e.Conversation(ctx, conversation)
	if err != nil {
		return nil, err
	}
	r, err := e.previewRepo()
	if err != nil {
		return nil, err
	}
	if len(ids) > 100 {
		return nil, ErrInvalid
	}
	messages := []domain.Message{}
	if len(ids) == 0 {
		messages, err = e.Messages(ctx, c.ID, "", 50)
		if err != nil {
			return nil, err
		}
	} else {
		seen := map[domain.ID]bool{}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			m, err := e.Message(ctx, id)
			if err != nil {
				return nil, err
			}
			if m.ConversationID != c.ID {
				return nil, ErrInvalid
			}
			messages = append(messages, m)
		}
	}
	out := []domain.MessagePreview{}
	for _, m := range messages {
		for _, address := range previewURLs(domain.DisplayBody(m)) {
			if knownNonPreviewFile(address) {
				continue
			}
			p, err := e.ensureMessagePreview(ctx, r, m, address)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	e.wakePreviews()
	return out, nil
}
func knownNonPreviewFile(address string) bool {
	for _, ref := range SharedReferences(address) {
		if ref.Kind == "video" || ref.Kind == "audio" || ref.Kind == "document" {
			return true
		}
	}
	return false
}
func (e *Engine) ensureMessagePreview(ctx context.Context, r PreviewRepository, m domain.Message, address string) (domain.MessagePreview, error) {
	if knownNonPreviewFile(address) {
		return domain.MessagePreview{}, ErrInvalid
	}
	id, err := e.newID()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	rev, err := e.newID()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	p := domain.MessagePreview{ID: id, RevisionID: rev, AccountID: m.AccountID, ConversationID: m.ConversationID, MessageID: m.ID, URL: address, PreviewVersion: previewRendererVersion, Kind: "link", State: "queued", DownloadState: "not_downloaded", UpdatedAt: e.now().UTC()}
	u, _ := url.Parse(address)
	ext := strings.ToLower(path.Ext(u.Path))
	if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" {
		p.Kind = "image"
		p.State = "ready"
		p.Title = path.Base(u.Path)
		if title := []rune(p.Title); len(title) > 300 {
			p.Title = string(title[:300])
		}
		p.SiteName = u.Hostname()
	}
	p, err = r.EnsurePreview(ctx, p)
	if err != nil {
		return domain.MessagePreview{}, err
	}
	if p.PreviewVersion < previewRendererVersion && (p.ImageHash != "" || p.Kind == "link") {
		old := p.RevisionID
		p.RevisionID, err = e.newID()
		if err != nil {
			return domain.MessagePreview{}, err
		}
		p.PreviewVersion = previewRendererVersion
		p.State = "queued"
		p.UpdatedAt = e.now().UTC()
		err = r.SavePreview(ctx, p, old)
		if errors.Is(err, ErrStateConflict) {
			p, err = r.Preview(ctx, p.ID)
		}
		if err != nil {
			return domain.MessagePreview{}, err
		}
	}

	return p, nil
}
func (e *Engine) Preview(ctx context.Context, id domain.ID) (domain.MessagePreview, error) {
	if !validID(id) {
		return domain.MessagePreview{}, ErrInvalid
	}
	r, err := e.previewRepo()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	return r.Preview(ctx, id)
}

// RefreshPreview queues fresh metadata without mutating source text or fetching
// an original. A persisted flag coalesces retries and survives engine restart.
func (e *Engine) RefreshPreview(ctx context.Context, id domain.ID) (domain.MessagePreview, error) {
	if _, ok := e.previewFetcher.(PreviewCacheInvalidator); !ok || e.mediaFiles == nil {
		return domain.MessagePreview{}, ErrProtocolUnavailable
	}
	r, err := e.previewRepo()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		p, err := e.Preview(ctx, id)
		if err != nil {
			return p, err
		}
		if p.Kind != "link" {
			return p, ErrInvalid
		}
		m, err := e.Message(ctx, p.MessageID)
		if err != nil {
			return p, err
		}
		current := false
		for _, ref := range SharedReferences(domain.DisplayBody(m)) {
			if ref.URL == p.URL && ref.Kind == "link" {
				current = true
				break
			}
		}
		if !current || m.AccountID != p.AccountID || m.ConversationID != p.ConversationID {
			return p, ErrNotFound
		}
		if p.RefreshRequested && p.State == "queued" {
			return p, nil
		}
		old := p.RevisionID
		p.RevisionID, err = e.newID()
		if err != nil {
			return p, err
		}
		p.State = "queued"
		p.RefreshRequested = true
		p.LastErrorCode = ""
		p.UpdatedAt = e.now().UTC()
		if err = r.SavePreview(ctx, p, old); errors.Is(err, ErrStateConflict) {
			continue
		} else if err != nil {
			return p, err
		}
		e.previewFetcher.(PreviewCacheInvalidator).InvalidateLink(p.URL)
		e.wakePreviews()
		return p, nil
	}
	return domain.MessagePreview{}, ErrStateConflict
}

func (e *Engine) DownloadPreview(ctx context.Context, id domain.ID) (domain.MessagePreview, error) {
	r, err := e.previewRepo()
	if err != nil {
		return domain.MessagePreview{}, err
	}
	if e.previewFetcher == nil || e.mediaFiles == nil {
		return domain.MessagePreview{}, ErrProtocolUnavailable
	}
	for i := 0; i < 5; i++ {
		p, err := e.Preview(ctx, id)
		if err != nil {
			return p, err
		}
		if p.Kind != "image" || knownNonPreviewFile(p.URL) {
			return p, ErrInvalid
		}
		if p.DownloadState == "ready" || p.DownloadState == "queued" || p.DownloadState == "downloading" {
			return p, nil
		}
		old := p.RevisionID
		p.RevisionID, err = e.newID()
		if err != nil {
			return p, err
		}
		p.DownloadState = "queued"
		p.LastErrorCode = ""
		p.UpdatedAt = e.now().UTC()
		err = r.SavePreview(ctx, p, old)
		if errors.Is(err, ErrStateConflict) {
			continue
		}
		if err != nil {
			return p, err
		}
		e.wakePreviews()
		return p, nil
	}
	return domain.MessagePreview{}, ErrStateConflict
}
func (e *Engine) MediaImage(ctx context.Context, hash string) ([]byte, domain.MediaFile, error) {
	if !ValidImageHash(hash) {
		return nil, domain.MediaFile{}, ErrInvalid
	}
	r, err := e.previewRepo()
	if err != nil {
		return nil, domain.MediaFile{}, err
	}
	file, err := r.MediaFile(ctx, hash)
	if err != nil {
		return nil, file, err
	}
	if e.mediaFiles == nil {
		return nil, file, ErrNotFound
	}
	data, err := e.mediaFiles.Read(ctx, file)
	return data, file, err
}
func (e *Engine) wakePreviews() {
	select {
	case e.previewWork <- struct{}{}:
	default:
	}
}
func previewFailureCode(err error) string {
	var f PreviewFailure
	if errors.As(err, &f) {
		switch f.Code {
		case "blocked", "not_found", "too_large", "unsupported", "fetch_failed":
			return f.Code
		}
	}
	return "fetch_failed"
}
func (e *Engine) runPreviews(ctx context.Context) error {
	r, err := e.previewRepo()
	if err != nil {
		return err
	}
	// A partial original is never served. Restart retries the same source reference.
	for {
		rows, err := r.InterruptedPreviewJobs(ctx, 100)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, p := range rows {
			old := p.RevisionID
			p.RevisionID, err = e.newID()
			if err != nil {
				return err
			}
			p.DownloadState = "queued"
			p.UpdatedAt = e.now().UTC()
			if err = r.SavePreview(ctx, p, old); err != nil && !errors.Is(err, ErrStateConflict) {
				return err
			}
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		jobs, err := r.PreviewJobs(ctx, e.now().UTC().Add(-30*time.Minute), 1)
		if err != nil {
			return err
		}
		if len(jobs) > 0 {
			if err = e.processPreview(ctx, r, jobs[0]); err != nil && !errors.Is(err, ErrStateConflict) {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			timer := time.NewTimer(300 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-e.previewWork:
		case <-ticker.C:
		}
	}
	return nil
}
func (e *Engine) processPreview(ctx context.Context, r PreviewRepository, p domain.MessagePreview) error {
	if knownNonPreviewFile(p.URL) {
		old := p.RevisionID
		var err error
		p.RevisionID, err = e.newID()
		if err != nil {
			return err
		}
		p.State = "unavailable"
		p.LastErrorCode = "unsupported"
		p.PreviewVersion = previewRendererVersion
		if p.DownloadState == "queued" || p.DownloadState == "downloading" {
			p.DownloadState = "failed"
		}
		p.UpdatedAt = e.now().UTC()
		return r.SavePreview(ctx, p, old)
	}
	original := p.DownloadState == "queued"
	old := p.RevisionID
	if original {
		var err error
		p.RevisionID, err = e.newID()
		if err != nil {
			return err
		}
		p.DownloadState = "downloading"
		p.UpdatedAt = e.now().UTC()
		if err = r.SavePreview(ctx, p, old); err != nil {
			return err
		}
		old = p.RevisionID
	}
	var fetchErr error
	var image, favicon []byte
	if original {
		image, fetchErr = e.previewFetcher.Image(ctx, p.URL)
		if fetchErr == nil {
			var file domain.MediaFile
			file, fetchErr = e.mediaFiles.Put(ctx, image, true)
			if fetchErr == nil {
				fetchErr = r.SaveMediaFile(ctx, file, e.now().UTC())
				if fetchErr == nil {
					p.FileHash = file.Hash
					p.FileMimeType = file.MimeType
					p.FileSize = file.Size
					p.DownloadState = "ready"
				}
			}
		}
	} else if p.Kind == "image" && p.DownloadState == "ready" && p.FileHash != "" {
		var file domain.MediaFile
		file, fetchErr = r.MediaFile(ctx, p.FileHash)
		if fetchErr == nil {
			image, fetchErr = e.mediaFiles.Read(ctx, file)
		}
		p.State = "ready"
	} else {
		var metadata PreviewMetadata
		if p.RefreshRequested {
			if invalidator, ok := e.previewFetcher.(PreviewCacheInvalidator); ok {
				invalidator.InvalidateLink(p.URL)
			} else {
				fetchErr = ErrProtocolUnavailable
			}
		}
		if fetchErr == nil {
			metadata, fetchErr = e.previewFetcher.Link(ctx, p.URL)
		}
		if fetchErr == nil {
			p.Title = metadata.Title
			p.Description = metadata.Description
			p.SiteName = metadata.SiteName
			p.State = "ready"
			image = metadata.Image
			favicon = metadata.Favicon
			p.ImageHash = ""
			p.FaviconHash = ""
			if metadata.IsImage {
				p.Kind = "image"
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if fetchErr == nil && len(image) > 0 {
		thumb, err := e.mediaFiles.Thumbnail(ctx, image)
		if err == nil {
			err = r.SaveMediaFile(ctx, thumb, e.now().UTC())
			if err != nil {
				return err
			}
			p.ImageHash = thumb.Hash
		} else if original {
			fetchErr = err
		}
	}
	if fetchErr == nil && len(favicon) > 0 {
		icon, iconErr := e.mediaFiles.Thumbnail(ctx, favicon)
		if iconErr == nil {
			if err := r.SaveMediaFile(ctx, icon, e.now().UTC()); err != nil {
				return err
			}
			p.FaviconHash = icon.Hash
		}
	}
	p.RefreshRequested = false
	if fetchErr != nil {
		p.LastErrorCode = previewFailureCode(fetchErr)
		if original {
			p.DownloadState = "failed"
		} else {
			p.State = "unavailable"
		}
	} else {
		p.LastErrorCode = ""
	}
	var err error
	p.PreviewVersion = previewRendererVersion
	p.RevisionID, err = e.newID()
	if err != nil {
		return err
	}
	p.UpdatedAt = e.now().UTC()
	return r.SavePreview(ctx, p, old)
}

// Run joins independent outbox and cache workers under one lifecycle.
func (e *Engine) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 4)
	workers := 2
	if _, ok := e.repo.(RoomIntentRepository); ok {
		if _, ok := e.protocols.(RoomProtocol); ok {
			workers++
			go func() { done <- e.runRooms(ctx) }()
		}
	}
	go func() { done <- e.runOutbox(ctx) }()
	go func() { done <- e.runSharedContent(ctx) }()
	if e.previewFetcher != nil && e.mediaFiles != nil {
		workers++
		go func() { done <- e.runPreviews(ctx) }()
	}
	var result error
	for i := 0; i < workers; i++ {
		err := <-done
		if err != nil && !errors.Is(err, context.Canceled) {
			result = errors.Join(result, err)
		}
		if i == 0 {
			cancel()
		}
	}
	return result
}
