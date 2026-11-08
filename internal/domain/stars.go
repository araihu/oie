package domain

import "time"

// MessageStar is owner metadata keyed by the source message UUID. Its revisions
// are separate from message content and delivery evidence. An empty revision
// denotes untouched initial state, which is never written by a read.
type MessageStar struct {
	MessageID, AccountID, ConversationID, RevisionID ID
	Starred                                          bool
	UpdatedAt                                        time.Time
}

type StarredMessage struct {
	Message      Message
	Conversation Conversation
	Star         MessageStar
}
