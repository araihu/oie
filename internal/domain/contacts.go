package domain

import "time"

// Contact is an observed provider roster identity. It is not inferred from
// messages or room occupants and does not implement app-owned identity linking.
type Contact struct {
	ID, RevisionID, AccountID          ID
	Address, DisplayName, Subscription string
	Groups                             []string
	Active                             bool
	UpdatedAt                          time.Time
	AccountLabel                       string `json:"-"`
	ConversationID                     ID     `json:"-"`
}

type ContactSource struct {
	AccountID ID
	State     string
	CheckedAt time.Time
}

type ContactQuery struct {
	AccountID                 ID
	AccountIDs                []ID
	Search, Cursor, ScopeHash string
	Limit                     int
}

type ContactPage struct {
	Items      []Contact
	Sources    []ContactSource
	NextCursor string
}
