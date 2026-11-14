package domain

import "time"

const (
	DefaultRoomRetentionDays = 180
	RoomRetentionMinimum     = 100
	MaxRetentionDays         = 365000
)

type GlobalRetention struct {
	Revision             string
	RoomDays, DirectDays int
}

type ConversationRetention struct {
	Revision      string
	Days          *int
	EffectiveDays int
	GlobalDays    int
}

type HistoryPruned struct {
	ConversationID ID
	MessageIDs     []ID
}

// RetentionCutoff is inclusive and measured in UTC days.
func RetentionCutoff(now time.Time, days int) time.Time {
	if days == 0 {
		return time.Time{}
	}
	return now.UTC().Truncate(time.Millisecond).AddDate(0, 0, -days)
}
