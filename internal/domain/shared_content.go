package domain

type SharedReference struct {
	ID, RevisionID      ID
	URL, Kind, FileName string
}
type SharedContentItem struct {
	SharedReference
	AccountLabel, ConversationTitle string
	Message                         Message
	Preview                         *MessagePreview
}
type SharedContentIndex struct {
	State                          string
	IndexedMessages, TotalMessages int64
}
type SharedContentPage struct {
	Items      []SharedContentItem
	NextCursor string
	Indexing   SharedContentIndex
}
type SharedContentMutation struct {
	Message       Message
	References    []SharedReference
	ParserVersion int
}
type SharedContentQuery struct {
	AccountID, ConversationID ID
	Type, Search, Cursor      string
	Limit                     int
}
