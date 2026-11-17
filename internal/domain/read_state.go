package domain

import "time"

// DisplayedMarker carries protocol-verified evidence for a newly read position.
type DisplayedMarker struct {
	ConversationID, AccountID, MessageID, PreferencesRevision ID
	RemoteID                                                  string
}

// ReadState is independent of message content and conversation activity.
type ReadState struct {
	ConversationID, AccountID, RevisionID ID
	UpdatedAt                             time.Time
	FirstUnreadMessageID                  ID `json:",omitempty"`
	UnreadCount                           int
}
