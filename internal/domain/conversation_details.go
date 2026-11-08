package domain

import "time"

type PresenceObservation struct {
	State, StatusText string
	ObservedAt        time.Time
}
type ConversationParticipant struct {
	ID, DisplayName, Source string
	Presence                PresenceObservation
}
type ConversationParticipants struct {
	Scope, State string
	Items        []ConversationParticipant
	Truncated    bool
	CheckedAt    time.Time
}
type ConversationInfo struct {
	State, Name, Description, Topic, RoomSession string
	CheckedAt                                    time.Time
	OccupantCount                                *int
	Features                                     []string
	Participants                                 ConversationParticipants
	PeerPresence                                 *PresenceObservation
}
type ConversationDetails struct {
	ConversationID, AccountID ID
	Kind, Address             string
	AccountConnection         Connection
	ConversationInfo
}

func EmptyConversationInfo(kind, state string) ConversationInfo {
	scope := "peer"
	session := ""
	if kind == "room" {
		scope = "occupants"
		session = "not_joined"
	}
	return ConversationInfo{State: state, RoomSession: session, Features: []string{}, Participants: ConversationParticipants{Scope: scope, State: state, Items: []ConversationParticipant{}}}
}
