package domain

import "time"

// ReadState is independent of message content and conversation activity.
type ReadState struct {
	ConversationID, AccountID, RevisionID ID
	UpdatedAt                             time.Time
	UnreadCount                           int
}
