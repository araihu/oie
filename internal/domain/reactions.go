package domain

import (
	"encoding/hex"
	"strings"
	"time"
)

const RoomReactionActorPrefix = "occupant:"

func IsRoomReactionActor(actor string) bool {
	hash, ok := strings.CutPrefix(actor, RoomReactionActorPrefix)
	if !ok || len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil && hash == strings.ToLower(hash)
}

// OutgoingReaction is an adapter-authorized frozen target and sender identity.
type OutgoingReaction struct {
	TargetMessageID      ID
	Reference            MessageReference
	Actor, SessionID     string
	RoomActor, ActorName string
	RoomIntentRevisionID ID
	MaxEmojis            int
	AllowedEmojis        []string
}

// ObservationActor is the identity stored for the submitted set and its echo.
func (p OutgoingReaction) ObservationActor() string {
	if p.RoomActor != "" {
		return p.RoomActor
	}
	return p.Actor
}

type ReactionItem struct {
	Emoji string
	Count int
	Own   bool
}
type ReactionActor struct {
	Address, DisplayName, AvatarHash string
	Own                              bool
	Emojis                           []string
}

type MessageReactions struct {
	MessageID, AccountID, ConversationID, RevisionID, OwnRevisionID ID
	OwnEmojis                                                       []string
	Items                                                           []ReactionItem
	Actors                                                          []ReactionActor
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
