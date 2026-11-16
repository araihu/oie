package domain

import "time"

// MessageEditHistoryItem is one retained content source, not a delivery transition.
type MessageEditHistoryItem struct {
	RevisionID, SourceMessageID ID
	CreatedAt                   time.Time
	OriginalText                string
	Attachments                 []StagedAttachment
	AttachmentLinks             []SharedReference
	Sender                      Sender
	IsCurrent, IsOriginal       bool
	ParentRevisionIDs           []ID
}

type MessageEditHistoryPage struct {
	RequestedMessageID, MessageID, AccountID, ConversationID, CurrentRevisionID ID
	Retraction                                                                  *MessageRetraction
	Snapshot, NextCursor                                                        string
	Items                                                                       []MessageEditHistoryItem
}
