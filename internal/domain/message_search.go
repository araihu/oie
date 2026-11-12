package domain

import "time"

type MessageSearchQuery struct {
	Search, Cursor, ScopeHash string
	AccountID, ConversationID ID
	IncludeEditHistory        bool
	Limit                     int
}

type MessageSearchHit struct {
	Message                         Message
	AccountLabel, ConversationTitle string
	SenderAvatarHash                string
	MatchedRevisionID               ID
	MatchedAt                       time.Time
	Historical                      bool
	MatchKind, Excerpt              string
}

type MessageSearchPage struct {
	Query                     string
	AccountID, ConversationID ID
	IncludeEditHistory        bool
	Items                     []MessageSearchHit
	NextCursor                string
	MetadataIndexing          SharedContentIndex
}
