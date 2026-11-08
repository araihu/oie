package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// A conversation fragment may reuse the inbox only within the same engine,
// account filter, query and presentation settings.
func (v view) conversationScope() string {
	data, _ := json.Marshal([]string{v.EngineID, v.AccountFilter, v.Section, v.Filter, v.Query, v.Locale, v.Theme})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// Compare server data rather than DOM enhanced by HTMX, avatars and local time.
func (v view) conversationListVersion() string {
	data, _ := json.Marshal([]any{v.Chats, v.Accounts, v.Locale, v.Section, v.Filter, v.Query})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
