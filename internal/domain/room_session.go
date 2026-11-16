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

// DisplayStatus summarizes the account's room session for conversation views.
func (s RoomSession) DisplayStatus() string {
	if s.State == "joined" {
		return "joined"
	}
	switch s.LastErrorCode {
	case "room_destroyed":
		return "destroyed"
	case "not_connected":
		return "disconnected"
	case "forbidden", "not_authorized", "registration_required", "removed":
		return "access_denied"
	}
	switch s.State {
	case "joining":
		if s.Active && s.CreationName != nil {
			return "creating"
		}
		return "joining"
	case "failed":
		return "unavailable"
	default:
		return "not_joined"
	}
}
