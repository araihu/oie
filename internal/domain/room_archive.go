package domain

import "time"

type RoomArchive struct {
	ConversationID ID
	Mode           string
	RoomArchiveProgress
}

// RoomArchiveProgress describes one contiguous backward recovery. Search
// hits and isolated timeline context do not change these checkpoints.
type DeferredRoomReactions struct {
	Start, End, Boundary time.Time
	Cursor               string
}

type RoomArchiveProgress struct {
	DeferredReactions                              DeferredRoomReactions
	WindowStart, WindowEnd                         time.Time
	Boundary, CaughtUpThrough                      time.Time
	Cursor                                         string
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
