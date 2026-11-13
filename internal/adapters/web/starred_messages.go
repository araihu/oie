package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func (s *Server) loadStarredMessages(ctx context.Context, account, conversation, cursor string) (*engineapi.StarredMessagePage, bool) {
	limit := 50
	params := &engineapi.ListStarredMessagesParams{Limit: &limit}
	if account != "" {
		params.AccountId = &account
	}
	if conversation != "" {
		params.ConversationId = &conversation
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

func (v view) starredPageView(page *engineapi.StarredMessagePage) view {
	v.Stars = make(map[string]engineapi.MessageStar, len(page.Items))
	v.StarsKnown = true
	for _, item := range page.Items {
		v.Stars[item.Message.Id] = item.Star
	}
	return v
}

func starredPageVersion(page *engineapi.StarredMessagePage, invalid bool) string {
	data, _ := json.Marshal([]any{page, invalid})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
