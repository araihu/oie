package domain

// SenderProfile contains only a published name for a separately established
// personal address. It does not turn a nickname or a self-declared JID into identity.
type SenderProfile struct {
	AccountID, ConversationID, MessageID ID
	State, PersonalAddress, FullName     string
}
