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
	PersonalAddress string `json:"-"`
	ID              ID
	DisplayName     string
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
	AttachmentWireText                        string               `json:",omitempty"`
	DispatchStarted                           bool                 `json:",omitempty"`
	ScheduledAt                               *time.Time           `json:",omitempty"`
	Composition                               *OutgoingComposition `json:",omitempty"`
	Attachments                               []StagedAttachment   `json:",omitempty"`
	Mentions                                  []MessageMention     `json:",omitempty"`
	MediaThumbnails                           []MediaThumbnail     `json:",omitempty"`
	Edited                                    bool                 `json:",omitempty"`
	EditedAt                                  time.Time            `json:",omitempty"`
	ProjectionSourceID                        ID                   `json:"-"`
	Correction                                *MessageCorrection   `json:"-"`
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

type OutgoingComposition struct {
	OriginalText  string
	ReplyTo       ID                 `json:",omitempty"`
	Mentions      []MentionSelection `json:",omitempty"`
	AttachmentIDs []ID               `json:",omitempty"`
}

type StagedAttachment struct {
	ID                          ID
	FileName, MediaType, SHA256 string
	Size                        int64
}

type HistoryStatus struct {
	AccountID                       ID
	State                           string
	RetainedMessages, Conversations int
	UpdatedAt                       time.Time
	LastErrorCode                   string
}
