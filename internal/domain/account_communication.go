package domain

// AccountCommunicationPreferences are shared by clients of the same engine.
type AccountCommunicationPreferences struct {
	RevisionID ID
	Presence   string
	SendTyping bool
}
