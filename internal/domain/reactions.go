package domain

import "time"

// OutgoingReaction is an adapter-authorized frozen target and sender identity.
type OutgoingReaction struct {
	TargetMessageID      ID
	Reference            MessageReference
	Actor, SessionID     string
	RoomIntentRevisionID ID
	MaxEmojis            int
	AllowedEmojis        []string
}

type ReactionItem struct {
	Emoji string
	Count int
	Own   bool
}
type MessageReactions struct {
	MessageID, AccountID, ConversationID, RevisionID, OwnRevisionID ID
	OwnEmojis                                                       []string
	Items                                                           []ReactionItem
	Eligible                                                        bool
	Reason                                                          string
	MaxEmojis                                                       int
	AllowedEmojis                                                   []string
	PendingCommand                                                  *MessageReactionCommand
}
type MessageReactionCommand struct {
	ID, RevisionID, MessageID, AccountID, ConversationID, ExpectedOwnRevisionID ID
	Emojis                                                                      []string
	State, LastErrorCode                                                        string
	CreatedAt, UpdatedAt                                                        time.Time
}
type PendingMessageReaction struct {
	Command MessageReactionCommand
	Plan    OutgoingReaction
}
