// Package domain contains protocol- and transport-independent identities and state.
package domain

import (
	"errors"
	"regexp"
	"time"
)

type ID string

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func ParseID(s string) (ID, error) {
	if !uuidV7.MatchString(s) {
		return "", errors.New("expected canonical UUIDv7")
	}
	return ID(s), nil
}

type Account struct {
	ID         ID
	RevisionID ID
	Protocol   string
	Address    string
	Label      string
	CreatedAt  time.Time
}

type Revision struct {
	ID        ID
	EntityID  ID
	Parents   []ID
	CreatedAt time.Time
}

// CurrentRevision selects among known heads. Unknown parents are retained as
// references; missing provider history must not be replaced by invented ancestry.
func CurrentRevision(revisions []Revision) (Revision, error) {
	if len(revisions) == 0 {
		return Revision{}, errors.New("empty revision history")
	}
	if _, err := ParseID(string(revisions[0].EntityID)); err != nil {
		return Revision{}, err
	}
	byID := make(map[ID]Revision, len(revisions))
	for _, r := range revisions {
		if _, err := ParseID(string(r.ID)); err != nil {
			return Revision{}, err
		}
		if r.EntityID != revisions[0].EntityID || r.CreatedAt.IsZero() {
			return Revision{}, errors.New("invalid revision entity or timestamp")
		}
		if _, exists := byID[r.ID]; exists {
			return Revision{}, errors.New("duplicate revision identity")
		}
		for _, parent := range r.Parents {
			if _, err := ParseID(string(parent)); err != nil {
				return Revision{}, err
			}
		}
		byID[r.ID] = r
	}
	colors := make(map[ID]uint8)
	superseded := make(map[ID]bool)
	var visit func(ID) error
	visit = func(id ID) error {
		if colors[id] == 1 {
			return errors.New("revision cycle")
		}
		if colors[id] == 2 {
			return nil
		}
		colors[id] = 1
		for _, parent := range byID[id].Parents {
			if _, exists := byID[parent]; exists {
				superseded[parent] = true
				if err := visit(parent); err != nil {
					return err
				}
			}
		}
		colors[id] = 2
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return Revision{}, err
		}
	}
	var winner Revision
	for _, r := range revisions {
		if superseded[r.ID] {
			continue
		}
		if winner.ID == "" || r.CreatedAt.After(winner.CreatedAt) ||
			(r.CreatedAt.Equal(winner.CreatedAt) && r.ID > winner.ID) {
			winner = r
		}
	}
	return winner, nil
}
