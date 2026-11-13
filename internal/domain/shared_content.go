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
type SharedContentFirstPageCounts struct {
	Media, Links, Documents int
}
type SharedContentPage struct {
	Items           []SharedContentItem
	NextCursor      string
	Indexing        SharedContentIndex
	FirstPageCounts *SharedContentFirstPageCounts
}
type SharedContentMutation struct {
	Message       Message
	References    []SharedReference
	ParserVersion int
}
type SharedContentQuery struct {
	MessageIDs, AccountIDs    []ID
	AccountID, ConversationID ID
	Type, Search, Cursor      string
	Limit                     int
	IncludeFirstPageCounts    bool
}
