package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// A conversation fragment may reuse the inbox only within the same engine,
// account filter, query and locale. Theme changes apply to the mounted DOM.
func (v view) conversationScope() string {
	data, _ := json.Marshal([]string{v.EngineID, v.AccountFilter, v.Section, v.Query, v.Locale})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// Compare server data rather than DOM enhanced by HTMX, avatars and local time.
func (v view) conversationListVersion() string {
	data, _ := json.Marshal([]any{v.Chats, v.Accounts, v.Locale, v.Section, v.Filter, v.Query})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// Enhancement changes local times and avatars, so compare server row data.
func (v view) conversationRowVersion(c engineapi.Conversation) string {
	data, _ := json.Marshal([]any{c, v.Accounts, v.Locale})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
