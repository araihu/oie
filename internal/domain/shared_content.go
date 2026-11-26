package domain

type SharedReference struct {
	ID, RevisionID      ID
	URL, Kind, FileName string
	Sticker             bool `json:",omitempty"`
}

// StickerFile selects one image for display. XEP-0447 sources of a marked
// file are alternate locations for the same artwork.
func StickerFile(files []SharedReference) (SharedReference, bool) {
	for _, file := range files {
		if file.Sticker && file.Kind == "image" {
			return file, true
		}
	}
	return SharedReference{}, false
}

type SharedContentItem struct {
	SharedReference
	AccountLabel, ConversationTitle string
	Message                         Message
	Preview                         *MessagePreview
	Star                            *MessageStar
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
	IncludeMessageStars       bool
	IncludeRetracted          bool
}
