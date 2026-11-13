package domain

// RoomPreview contains published information available before joining a room.
type RoomPreview struct {
	Address, State, Name, Description, Topic, AvatarHash string
	OccupantCount                                        *int
}
