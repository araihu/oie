package domain

import "time"

// RoomSession separates durable local join intent from a provider observation.
// Active is owned by the engine; only a protocol's own-presence confirmation
// permits State joined.
type RoomSession struct {
	AccountID, ConversationID, IntentID, IntentRevisionID ID
	Active                                                bool
	CreationName                                          *string `json:"-"`
	Revoked                                               bool    `json:"-"`
	State, Nickname, LastErrorCode                        string
	UpdatedAt                                             time.Time
}
