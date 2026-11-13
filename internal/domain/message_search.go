package domain

import "time"

type MessageSearchQuery struct {
	Search, Cursor, ScopeHash string
	AccountID, ConversationID ID
	AccountIDs                []ID
	IncludeEditHistory        bool
	Limit                     int
}

type MessageSearchHit struct {
	Message                                              Message
	AccountLabel, ConversationTitle, ConversationAddress string
	SenderAddress, SenderAvatarHash                      string
	MatchedRevisionID                                    ID
	MatchedAt                                            time.Time
	Historical                                           bool
	MatchKind, Excerpt                                   string
	Preview                                              *MessageSearchPreview
}

type MessageSearchPreview struct {
	Kind, URL, ImageHash, FaviconHash string
}

type MessageSearchPage struct {
	Query                     string
	AccountID, ConversationID ID
	AccountIDs                []ID
	IncludeEditHistory        bool
	Items                     []MessageSearchHit
	NextCursor                string
	MetadataIndexing          SharedContentIndex
}
