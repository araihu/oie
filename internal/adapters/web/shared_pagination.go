package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

// A cursor identifies one page; the scope identifies the collection that may
// receive it. Drawer navigation keeps the surrounding inbox filters intact.
func (v view) sharedPageScope() string {
	conversation, query := v.SharedConversation, v.Query
	accounts := v.sharedCatalogAccounts()
	if v.SharedInDetails {
		query = v.InfoQuery
		if v.Selected != nil {
			accounts = []string{v.Selected.AccountId}
			conversation = v.Selected.Id
		}
	}
	if len(accounts) == 0 {
		accounts = nil
	}
	data, _ := json.Marshal([]any{v.EngineID, v.SharedInDetails, accounts, conversation, v.SharedType, query, v.SharedIncludeRetracted, v.Locale})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (v view) sharedPageState() string {
	if v.Unavailable {
		return "unavailable"
	}
	if v.SharedInvalid {
		return "invalid"
	}
	if v.SharedContent == nil {
		return "unavailable"
	}
	return "ready"
}

func (v view) sharedFirstPageCounts() *engineapi.SharedContentFirstPageCounts {
	if v.sharedPageState() != "ready" {
		return nil
	}
	counts := v.SharedContent.FirstPageCounts
	if counts == nil {
		return nil
	}
	for _, count := range []int{counts.Media, counts.Links, counts.Documents} {
		if count < 0 || count > sharedContentPageSize {
			return nil
		}
	}
	return counts
}

func (v view) sharedPageNext() string {
	if v.SharedContent != nil && v.SharedContent.NextCursor != nil {
		return *v.SharedContent.NextCursor
	}
	return ""
}
