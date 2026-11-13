package domain

import "time"

type RoomManagement struct {
	State, Revision string
	Actions         []string
	Occupants       []RoomManagedOccupant
	Affiliations    []string
}

type RoomManagedOccupant struct {
	ID, Nickname, Role, Affiliation string
	Roles, Affiliations             []string
}

type RoomManagementCommand struct {
	Action, ExpectedRevision, Target, Value, Reason string
	MessageID                                       ID
	Name                                            *string `json:",omitempty"`
	Image                                           []byte  `json:",omitempty"`
	Topic                                           *string `json:",omitempty"`
}

type MessageModeration struct {
	At     time.Time
	Reason string
}
