package domain

import "time"

// DisplayedMarker carries protocol-verified evidence for a newly read position.
type DisplayedMarker struct {
	ConversationID, AccountID, MessageID ID
	PolicyEpoch, RemoteID, Transport     string
}

// ReadState is independent of message content and conversation activity.
type ReadState struct {
	ConversationID, AccountID, RevisionID ID
	UpdatedAt                             time.Time
	FirstUnreadMessageID                  ID `json:",omitempty"`
	UnreadCount                           int
}

type GlobalReadReceipts struct {
	Revision string
	Enabled  bool
}

type ConversationReadReceipts struct {
	Revision, PolicyEpoch           string
	Override                        *bool
	EffectiveEnabled, GlobalEnabled bool
}
