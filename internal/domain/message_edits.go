package domain

import "time"

// OutgoingCorrection freezes adapter authority and the original wire reference.
// Reconnecting never grants a queued correction a different sending identity.
type OutgoingCorrection struct {
	TargetMessageID                          ID
	OriginalWireID, Scope, Author, SessionID string
	RoomIntentRevisionID                     ID
}

type MessageEditTarget struct {
	MessageID, AccountID, ConversationID, CurrentRevisionID ID
	OriginalText                                            string
	Eligible                                                bool
	Reason                                                  string
}

type MessageEditCommand struct {
	ID, RevisionID, MessageID, AccountID, ConversationID, ExpectedRevisionID ID
	OriginalText, State, LastErrorCode                                       string
	CreatedAt, UpdatedAt                                                     time.Time
}

// PendingMessageEdit includes private frozen dispatch data, never HTTP fields.
type PendingMessageEdit struct {
	Command           MessageEditCommand
	Plan              OutgoingCorrection
	ContentRevisionID ID
	Reply             *OutboundReply `json:",omitempty"`
	WireText          string         `json:",omitempty"`
	WireBodySHA256    string         `json:",omitempty"`
}

func (p PendingMessageEdit) WireBody() string {
	if p.WireText != "" {
		return p.WireText
	}
	return p.Command.OriginalText
}

// WireBodyHash stays bound to the original wire text when retention removes its quote.
func (p PendingMessageEdit) WireBodyHash() string {
	return (Message{OriginalText: p.WireBody(), WireBodySHA256: p.WireBodySHA256}).SourceBodyHash()
}

func (p PendingMessageEdit) MatchesWireBody(text string) bool {
	return p.WireBodyHash() == (Message{OriginalText: text}).SourceBodyHash()
}
