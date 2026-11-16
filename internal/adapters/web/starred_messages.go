package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/icon/heroicons"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) loadStarredMessages(ctx context.Context, account, conversation, cursor, query string) (*engineapi.StarredMessagePage, bool) {
	limit := 50
	params := &engineapi.ListStarredMessagesParams{Limit: &limit}
	if account != "" {
		params.AccountId = &account
	}
	if conversation != "" {
		params.ConversationId = &conversation
	}
	if query != "" {
		params.Q = &query
	}
	if cursor != "" {
		params.Cursor = &cursor
	}
	result, err := s.client.ListStarredMessagesWithResponse(ctx, params)
	if err != nil {
		return nil, false
	}
	if cursor != "" && (result.StatusCode() == 400 || result.StatusCode() == 409 || result.StatusCode() == 404) {
		return nil, true
	}
	if result.JSON200 == nil {
		return nil, false
	}
	for _, item := range result.JSON200.Items {
		if !validStarredSource(item, account, conversation) {
			return nil, false
		}
	}
	return result.JSON200, false
}

func validStarredSource(item engineapi.StarredMessage, account, conversation string) bool {
	m, chat, star := item.Message, item.Conversation, item.Star
	return validOperationKey(m.Id) && validOperationKey(m.RevisionId) &&
		validOperationKey(chat.Id) && validOperationKey(chat.AccountId) &&
		(account == "" || chat.AccountId == account) && (conversation == "" || chat.Id == conversation) &&
		m.ConversationId == chat.Id && m.AccountId == chat.AccountId &&
		star.MessageId == m.Id && star.AccountId == chat.AccountId && star.ConversationId == chat.Id &&
		star.Starred && star.RevisionId != nil && validOperationKey(*star.RevisionId)
}

func (v view) favoritesHref(cursor string) string {
	return v.href("section", "favorites", "chat", "", "q", "", "filter", "", "favoritesCursor", cursor, "lang", v.Locale, "theme", v.Theme)
}

type starredContent struct {
	messages map[string]messageView
	previews map[string][]engineapi.MessagePreview
	videos   map[string][]engineapi.SharedContentItem
}

func (s *Server) loadStarredContent(ctx context.Context, v *view, page *engineapi.StarredMessagePage) {
	if page == nil || len(page.Items) == 0 {
		return
	}

	type batch struct {
		conversation engineapi.Conversation
		messages     []messageView
	}
	batches := make(map[string]*batch)
	content := starredContent{
		messages: make(map[string]messageView, len(page.Items)),
		previews: make(map[string][]engineapi.MessagePreview),
		videos:   make(map[string][]engineapi.SharedContentItem),
	}
	for _, item := range page.Items {
		m := messageView{Message: item.Message}
		b := batches[m.ConversationId]
		if b == nil {
			b = &batch{conversation: item.Conversation}
			batches[m.ConversationId] = b
		}
		b.messages = append(b.messages, m)
	}

	// Favorites can span dozens of conversations. Limit concurrent enrichment
	// and let the retained body and attachments render if a preview is unavailable.
	enrichment, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	jobs := make(chan *batch, len(batches))
	for _, b := range batches {
		jobs <- b
	}
	close(jobs)
	var workers sync.WaitGroup
	var merge sync.Mutex
	for range min(6, len(batches)) {
		workers.Go(func() {
			for b := range jobs {
				part := view{Selected: &b.conversation, Preview: v.Preview, Locale: v.Locale, MessagePreviews: make(map[string][]engineapi.MessagePreview)}
				for i := range b.messages {
					m := &b.messages[i]
					m.PresentationKnown = !messageBodyURLs.MatchString(m.displayBody())
					if !m.PresentationKnown {
						part.Messages = append(part.Messages, *m)
					}
				}
				if enrichment.Err() == nil {
					s.loadMessagePreviews(enrichment, &part)
					s.loadMessageVideos(enrichment, &part)
				}
				for i := range b.messages {
					m := &b.messages[i]
					m.VideosKnown = m.PresentationKnown || part.MessageVideos != nil
					m.PresentationKnown = m.PresentationKnown || part.PreviewsKnown
					part.formatMessage(ctx, m)
				}
				merge.Lock()
				for _, m := range b.messages {
					content.messages[m.Id] = m
					content.previews[m.Id] = part.MessagePreviews[m.Id]
					content.videos[m.Id] = part.MessageVideos[m.Id]
				}
				merge.Unlock()
			}
		})
	}
	workers.Wait()
	v.StarredContent = content
}

func (v view) starredPageView(page *engineapi.StarredMessagePage, scope string) view {
	v.Stars = make(map[string]engineapi.MessageStar, len(page.Items))
	v.StarsKnown = true
	v.PreviewScope = scope
	v.MessagePreviews = v.StarredContent.previews
	v.MessageVideos = v.StarredContent.videos
	for _, item := range page.Items {
		v.Stars[item.Message.Id] = item.Star
	}
	return v
}

func (v view) starredMessage(item engineapi.StarredMessage) messageView {
	if m, ok := v.StarredContent.messages[item.Message.Id]; ok {
		return m
	}
	return messageView{Message: item.Message}
}

func (v view) starredRowVersion(item engineapi.StarredMessage) string {
	m := v.starredMessage(item)
	data, _ := json.Marshal([]any{item, v.accountLabel(item.Message.AccountId), m.PresentationKnown, m.VideosKnown, v.MessagePreviews[item.Message.Id], v.messageVideos(item.Message.Id)})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (v view) starredPageVersion(page *engineapi.StarredMessagePage, invalid bool) string {
	var revisions []string
	if page != nil {
		v.MessagePreviews = v.StarredContent.previews
		v.MessageVideos = v.StarredContent.videos
		for _, item := range page.Items {
			revisions = append(revisions, v.starredRowVersion(item))
		}
	}
	data, _ := json.Marshal([]any{page, invalid, revisions})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func favoriteActions(v view, message engineapi.Message, scope string) []dropdown.Section {
	prefix := scope + "-" + message.Id
	items := []dropdown.Item{
		{ID: "favorite-source-" + prefix, Label: v.Text.SharedGoToMessage, Href: v.infoSourceHref(message), Icon: goToMessageIcon()},
	}
	if message.Retraction == nil && message.Moderation == nil {
		items = append(items, dropdown.Item{ID: "favorite-copy-" + prefix, Label: v.Text.CopyOriginal, OnClick: "void 0", Icon: glyph(heroicons.Icon16SolidDocumentDuplicate)})
		if !v.Preview && message.SendState != "cancelled" {
			items = append(items, forwardAction(v, "favorite-"+scope+"-", message.Id))
		}
	}
	return []dropdown.Section{{Items: items}}
}
