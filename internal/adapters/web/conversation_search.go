package web

import _ "embed"

// The toolbar composes public Goshtoso textinput, checkbox and button controls.
// Search result identity, paging and bounded timeline focus remain app-owned.
//
//go:embed conversation-search.js
var conversationSearchScript []byte

//go:embed conversation-search.css
var conversationSearchStylesheet []byte

func (v view) conversationSearchCloseHref() string {
	return v.href("chatSearch", "", "chatQuery", "", "chatIncludeHistory", "")
}
