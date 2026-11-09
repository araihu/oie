package domain

import "time"

// MessageEditHistoryItem is one retained content source, not a delivery transition.
type MessageEditHistoryItem struct {
	RevisionID, SourceMessageID ID
	CreatedAt                   time.Time
	OriginalText                string
	Sender                      Sender
	IsCurrent, IsOriginal       bool
	ParentRevisionIDs           []ID
}

type MessageEditHistoryPage struct {
	RequestedMessageID, MessageID, AccountID, ConversationID, CurrentRevisionID ID
	Snapshot, NextCursor                                                        string
	Items                                                                       []MessageEditHistoryItem
}
