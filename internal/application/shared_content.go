package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/domain"
)

const SharedContentVersion = 1

type SharedContentRepository interface {
	SharedContentCandidates(context.Context, int, int) ([]domain.Message, error)
	IndexSharedContent(context.Context, []domain.SharedContentMutation) error
	SharedContent(context.Context, domain.SharedContentQuery) (domain.SharedContentPage, error)
}

// SharedReferences classifies references locally; it never retrieves a URL.
// A reference retains source context even when another message shares the URL.
func SharedReferences(text string) []domain.SharedReference {
	out := []domain.SharedReference{}
	seen := map[string]bool{}
	for _, candidate := range messageURLs.FindAllString(text, 128) {
		address := strings.TrimRight(candidate, ".,;!)]}")
		if len(address) > 4096 || seen[address] || !utf8.ValidString(address) {
			continue
		}
		u, err := url.Parse(address)
		if err != nil || u.Hostname() == "" || u.User != nil {
			continue
		}
		seen[address] = true
		kind := "link"
		extension := strings.ToLower(path.Ext(u.Path))
		switch extension {
		case ".png", ".jpg", ".jpeg", ".gif":
			kind = "image"
		case ".mp4", ".m4v", ".webm", ".mov", ".mkv", ".avi":
			kind = "video"
		case ".mp3", ".ogg", ".opus", ".m4a", ".wav", ".flac", ".aac":
			kind = "audio"
		case ".pdf", ".doc", ".docx", ".odt", ".xls", ".xlsx", ".ods", ".ppt", ".pptx", ".odp", ".txt", ".csv", ".md", ".zip", ".tar", ".gz", ".7z", ".rar", ".epub":
			kind = "document"
		}
		name := path.Base(u.Path)
		if name == "." || name == "/" || name == "" {
			name = u.Hostname()
		}
		if chars := []rune(name); len(chars) > 300 {
			name = string(chars[:300])
		}
		out = append(out, domain.SharedReference{URL: address, Kind: kind, FileName: name})
		if len(out) == 32 {
			break
		}
	}
	return out
}

type SharedCursor struct {
	Milliseconds int64     `json:"ms"`
	ID           domain.ID `json:"id"`
}

func SharedContentCursor(ms int64, id domain.ID) string {
	b, _ := json.Marshal(SharedCursor{ms, id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func ParseSharedContentCursor(value string) (SharedCursor, error) {
	var cursor SharedCursor
	if value == "" {
		return cursor, nil
	}
	if len(value) > 512 {
		return cursor, ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, ErrInvalid
	}
	if json.Unmarshal(b, &cursor) != nil || !validID(cursor.ID) {
		return SharedCursor{}, ErrInvalid
	}
	return cursor, nil
}
func (e *Engine) SharedContent(ctx context.Context, q domain.SharedContentQuery) (domain.SharedContentPage, error) {
	empty := domain.SharedContentPage{Items: []domain.SharedContentItem{}}
	if q.Type == "" {
		q.Type = "media"
	}
	if q.Type != "media" && q.Type != "links" && q.Type != "documents" || q.Limit < 1 || q.Limit > 100 || !utf8.ValidString(q.Search) || utf8.RuneCountInString(q.Search) > 200 {
		return empty, ErrInvalid
	}
	if _, err := ParseSharedContentCursor(q.Cursor); err != nil {
		return empty, err
	}
	if q.AccountID != "" {
		if _, err := e.Account(ctx, q.AccountID); err != nil {
			return empty, err
		}
	}
	if q.ConversationID != "" {
		c, err := e.Conversation(ctx, q.ConversationID)
		if err != nil {
			return empty, err
		}
		if q.AccountID != "" && c.AccountID != q.AccountID {
			return empty, ErrInvalid
		}
	}
	r, ok := e.repo.(SharedContentRepository)
	if !ok {
		return empty, ErrProtocolUnavailable
	}
	page, err := r.SharedContent(ctx, q)
	if err != nil {
		return empty, err
	}
	previews, err := e.previewRepo()
	if err != nil {
		return page, nil
	}
	for i := range page.Items {
		item := &page.Items[i]
		if item.Kind != "image" && item.Kind != "link" {
			item.Preview = nil
			continue
		}
		p, err := e.ensureMessagePreview(ctx, previews, item.Message, item.URL)
		if err != nil {
			return empty, err
		}
		item.Preview = &p
	}
	e.wakePreviews()
	return page, nil
}

// Indexing remains local and separate from the outbox and protocol archive reader.
func (e *Engine) runSharedContent(ctx context.Context) error {
	r, ok := e.repo.(SharedContentRepository)
	if !ok {
		<-ctx.Done()
		return nil
	}
	for ctx.Err() == nil {
		candidates, err := r.SharedContentCandidates(ctx, SharedContentVersion, 64)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		batch := make([]domain.SharedContentMutation, 0, len(candidates))
		for _, m := range candidates {
			refs := SharedReferences(domain.DisplayBody(m))
			for i := range refs {
				refs[i].ID, err = e.newID()
				if err != nil {
					return err
				}
				refs[i].RevisionID, err = e.newID()
				if err != nil {
					return err
				}
			}
			batch = append(batch, domain.SharedContentMutation{Message: m, References: refs, ParserVersion: SharedContentVersion})
		}
		if len(batch) > 0 {
			err = r.IndexSharedContent(ctx, batch)
			if err != nil && !errors.Is(err, ErrStateConflict) {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		delay := 100 * time.Millisecond
		if len(batch) == 0 {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
