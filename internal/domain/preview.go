package domain

import "time"

// MessagePreview is source-linked derived metadata, independent of message text.
type MessagePreview struct {
	ID, RevisionID, AccountID, ConversationID, MessageID ID
	SharedContentID                                      ID
	URL, Kind, State, DownloadState                      string
	Title, Description, SiteName                         string
	ImageHash, FaviconHash, FileHash, FileMimeType       string
	PreviewVersion                                       int
	RefreshRequested                                     bool
	FileSize                                             int64
	LastErrorCode                                        string
	UpdatedAt                                            time.Time
}

type MediaFile struct {
	Hash, MimeType string
	Size           int64
}
