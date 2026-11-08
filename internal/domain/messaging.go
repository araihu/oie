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
type Message struct {
	ID, RevisionID, AccountID, ConversationID ID
	Sender                                    Sender
	OriginalText                              string
	CreatedAt                                 time.Time
	IsOwn                                     bool
	SendState                                 string
	ProviderID                                string
	ArchiveScope, ArchiveID                   string
	Historical                                bool
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
