package domain

import "time"

type RoomArchive struct {
	ConversationID ID
	Mode           string
	RoomArchiveProgress
}

// ArchiveReactionAnchor links equal timestamps across contiguous MAM pages.
type ArchiveReactionAnchor struct {
	At time.Time
	ID string
}

type DeferredRoomReactions struct {
	Start, End, Boundary time.Time
	Cursor               string
	Anchor               ArchiveReactionAnchor
}

// RoomArchiveProgress describes one contiguous backward recovery. Search
// hits and isolated timeline context do not change these checkpoints.
type RoomArchiveProgress struct {
	RetentionDays                                  *int `json:",omitempty"`
	OlderCoveredThrough                            time.Time
	DeferredReactions                              DeferredRoomReactions
	WindowStart, WindowEnd                         time.Time
	Boundary, CaughtUpThrough                      time.Time
	Cursor                                         string
	OlderReactionAnchor, CatchupReactionAnchor     ArchiveReactionAnchor
	ResumeEnd                                      time.Time
	CatchupStart, CatchupEnd, CatchupBoundary      time.Time
	CatchupCursor                                  string
	RecentComplete, InitialComplete, OlderComplete bool
	Availability, TextSearch                       string
}

type RoomArchiveRequest struct {
	Action, Query, Cursor string
	Anchor                *Message
}

type RoomArchiveResult struct {
	Archive    RoomArchive
	Outcome    string
	Items      []Message
	NextCursor string
}

func (progress *RoomArchiveProgress) ApplyRetention(days int, now time.Time) bool {
	if progress.RetentionDays != nil && *progress.RetentionDays == days {
		return false
	}
	cutoff := RetentionCutoff(now, days)
	if progress.RetentionDays != nil {
		previous := *progress.RetentionDays
		if previous > 0 && (days == 0 || days > previous) {
			if progress.OlderComplete && !progress.OlderCoveredThrough.IsZero() {
				progress.ResumeEnd = progress.OlderCoveredThrough
				if oldCutoff := RetentionCutoff(now, previous); oldCutoff.After(progress.ResumeEnd) {
					progress.ResumeEnd = oldCutoff
				}
			}
			progress.OlderComplete = false
		}
	}
	if !cutoff.IsZero() && (!progress.Boundary.IsZero() && progress.Boundary.Before(cutoff) ||
		progress.OlderComplete && (progress.OlderCoveredThrough.IsZero() || progress.OlderCoveredThrough.Before(cutoff))) {
		progress.Boundary = cutoff
		progress.ResumeEnd = cutoff
		progress.Cursor = ""
		progress.OlderReactionAnchor = ArchiveReactionAnchor{}
		if progress.RecentComplete {
			progress.OlderComplete, progress.InitialComplete = true, true
			progress.OlderCoveredThrough = cutoff
		}
	}
	progress.RetentionDays = &days
	return true
}
