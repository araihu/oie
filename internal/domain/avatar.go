package domain

import "time"

// Avatar is an observed profile projection, separate from personal identity linking.
type Avatar struct {
	ID, RevisionID, AccountID       ID
	Address, ImageHash, SourceToken string
	UpdatedAt                       time.Time
}
type AvatarTarget struct{ Address, Kind string }
