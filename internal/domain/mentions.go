package domain

// MentionSelection names an observed participant and a Unicode code-point span
// in the authored body, before any protocol compatibility prefix.
type MentionSelection struct {
	ParticipantID string
	Begin, End    int
}

// MessageMention freezes the accepted selection and adapter-selected target.
// URI is private protocol data and is never accepted from an HTTP client.
type MessageMention struct {
	MentionSelection
	ReferenceMode string
	URI           string `json:",omitempty"`
}

type MentionCandidate struct {
	ID, DisplayName, InsertText, ReferenceMode string
	URI                                        string
}
type MentionCandidates struct {
	ConversationID, AccountID ID
	State                     string
	Items                     []MentionCandidate
	Truncated                 bool
}
