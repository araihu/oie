package domain

import "time"

type Connection struct {
	AccountID     ID
	State         string
	UpdatedAt     time.Time
	LastErrorCode string
}

// LatestMessagePageSize is the default message page and the inbox estimate limit.
const LatestMessagePageSize = 50

type Conversation struct {
	ID, RevisionID, AccountID, PeerID ID
	Kind, Address, Title, LastMessage string
	UpdatedAt                         time.Time
	UnreadCount                       int
	LatestMessagePageCount            *int         `json:"-"`
	RoomSession                       *RoomSession `json:"-"`
	UnreadReminder                    bool         `json:",omitempty"`
	UnreadMention                     bool         `json:",omitempty"`
	RemovedFromChats                  bool         `json:",omitempty"`
	Archived, Favorite                bool
	ActivitySequence                  int64
	Request                           string              `json:",omitempty"`
	DirectHistoryCheck                *DirectHistoryCheck `json:",omitempty"`
}

func (c Conversation) NeedsRoomTitle() bool {
	return c.Kind == "room" && (c.Title == "" || c.Title == c.Address)
}

// AcceptsRoomTitle gives a room's advertised name precedence over fallback
// labels from bookmarks or its profile.
func (c Conversation) AcceptsRoomTitle(title string, fallback bool) bool {
	return c.Kind == "room" && title != "" && title != c.Address && title != c.Title &&
		(!fallback || c.NeedsRoomTitle())
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

type MessageRetraction struct {
	At                time.Time
	OriginalAvailable bool
}

func (m Message) IsWithdrawn() bool {
	return m.Moderation != nil || m.Retraction != nil
}

// EncryptedAttachment is private to the engine. Only its name is projected
// through the API. The URL never contains the key or nonce.
type EncryptedAttachment struct {
	URL, FileName string
	Key, Nonce    []byte
}

type Message struct {
	OMEMOWireDigests                          []string              `json:",omitempty"`
	EncryptedAttachments                      []EncryptedAttachment `json:",omitempty"`
	DirectEncryption                          string                `json:",omitempty"`
	Retraction                                *MessageRetraction    `json:",omitempty"`
	Moderation                                *MessageModeration    `json:",omitempty"`
	ID, RevisionID, AccountID, ConversationID ID
	Sender                                    Sender
	OriginalText                              string
	WireBodySHA256                            string               `json:",omitempty"`
	AttachmentWireText                        string               `json:",omitempty"`
	DispatchStarted                           bool                 `json:",omitempty"`
	ScheduledAt                               *time.Time           `json:",omitempty"`
	Composition                               *OutgoingComposition `json:",omitempty"`
	Attachments                               []StagedAttachment   `json:",omitempty"`
	Mentions                                  []MessageMention     `json:",omitempty"`
	WireMentions                              WireMentionEvidence  `json:",omitempty"`
	WireMentionDigest                         string               `json:",omitempty"`
	MentionsYou                               bool                 `json:",omitempty"`
	FileReferences                            []SharedReference    `json:",omitempty"`
	AudioURLs                                 []string             `json:",omitempty"`
	FileDigests                               []FileDigest         `json:",omitempty"`
	MediaThumbnails                           []MediaThumbnail     `json:",omitempty"`
	Edited                                    bool                 `json:",omitempty"`
	EditedAt                                  time.Time            `json:",omitempty"`
	ProjectionSourceID                        ID                   `json:"-"`
	Correction                                *MessageCorrection   `json:"-"`
	CreatedAt                                 time.Time
	TimelineAt                                *time.Time `json:",omitempty"`
	IsOwn                                     bool
	SendState                                 string
	ProviderID                                string
	OwnOriginID                               ID     `json:",omitempty"` // Verified original wire ID from an own echo.
	ReadReceiptID                             string `json:",omitempty"`
	ArchiveScope, ArchiveID                   string
	Historical                                bool
	ReceivedAt                                time.Time          `json:"-"`
	OutboundReply                             *OutboundReply     `json:",omitempty"`
	References                                []MessageReference `json:"-"`
	ReplyReference                            *ReplyReference    `json:"-"`
	Reply                                     *MessageReply      `json:"-"`
	// ObservedRoomNickname is internal evidence for classifying later edits.
	ObservedRoomNickname string `json:",omitempty"`
}

type OutgoingComposition struct {
	OriginalText  string
	ReplyTo       ID                 `json:",omitempty"`
	Mentions      []MentionSelection `json:",omitempty"`
	AttachmentIDs []ID               `json:",omitempty"`
}

func (m Message) TimelineTime() time.Time {
	if m.TimelineAt != nil {
		return *m.TimelineAt
	}
	return m.CreatedAt
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
