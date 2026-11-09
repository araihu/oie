package domain

import "time"

type Connection struct {
	AccountID     ID
	State         string
	UpdatedAt     time.Time
	LastErrorCode string
}

type Conversation struct {
	ID, RevisionID, AccountID, PeerID ID
	Kind, Address, Title, LastMessage string
	UpdatedAt                         time.Time
	UnreadCount                       int
	Archived, Favorite                bool
	ActivitySequence                  int64
}
type Sender struct {
	ID          ID
	DisplayName string
}

// MediaThumbnail binds an advertised small preview to its exact source URL.
type MediaThumbnail struct {
	URL          string
	ThumbnailURL string
}

type Message struct {
	ID, RevisionID, AccountID, ConversationID ID
	Sender                                    Sender
	OriginalText                              string
	Mentions                                  []MessageMention   `json:",omitempty"`
	MediaThumbnails                           []MediaThumbnail   `json:",omitempty"`
	Edited                                    bool               `json:",omitempty"`
	EditedAt                                  time.Time          `json:",omitempty"`
	ProjectionSourceID                        ID                 `json:"-"`
	Correction                                *MessageCorrection `json:"-"`
	CreatedAt                                 time.Time
	IsOwn                                     bool
	SendState                                 string
	ProviderID                                string
	ArchiveScope, ArchiveID                   string
	Historical                                bool
	OutboundReply                             *OutboundReply     `json:",omitempty"`
	References                                []MessageReference `json:"-"`
	ReplyReference                            *ReplyReference    `json:"-"`
	Reply                                     *MessageReply      `json:"-"`
}

type HistoryStatus struct {
	AccountID                       ID
	State                           string
	RetainedMessages, Conversations int
	UpdatedAt                       time.Time
	LastErrorCode                   string
}
