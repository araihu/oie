package domain

const (
	MessageRequest = "message"
	RoomInvitation = "room_invitation"
)

// DirectHistoryCheck is retained until the first message's archive lookup or a
// user decision settles. Only the request kind is exposed to clients.
type DirectHistoryCheck struct {
	ArchiveID  string
	ProviderID string
}
