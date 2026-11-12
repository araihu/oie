package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MentionBoundary follows Gajim's nickname delimiters and keeps Unicode marks
// attached to the surrounding text. Zero is the boundary of the message.
func MentionBoundary(before, after rune) bool {
	return !unicode.IsLetter(before) && !unicode.IsMark(before) && !strings.ContainsRune("/-'", before) &&
		!unicode.IsLetter(after) && !unicode.IsMark(after)
}

func RoomNicknameMention(nickname, body string, fallback []TextRange) bool {
	if nickname == "" {
		return false
	}
	body, _ = ReplyBody(body, fallback)
	body = strings.ToLower(body)
	nickname = strings.ToLower(nickname)
	_, firstSize := utf8.DecodeRuneInString(nickname)
	for offset := 0; offset < len(body); {
		index := strings.Index(body[offset:], nickname)
		if index < 0 {
			return false
		}
		start := offset + index
		before, _ := utf8.DecodeLastRuneInString(body[:start])
		after, _ := utf8.DecodeRuneInString(body[start+len(nickname):])
		if MentionBoundary(before, after) {
			return true
		}
		offset = start + firstSize
	}
	return false
}

// MentionSelection names an observed participant and a Unicode code-point span
// in the authored body, before any protocol compatibility prefix.
type MentionSelection struct {
	ParticipantID string
	Begin, End    int
}

// MessageMention freezes the accepted selection and adapter-selected target.
// URI is private protocol data and is never accepted from an HTTP client.
type MessageMention struct {
	MentionSelection
	ReferenceMode string
	URI           string `json:",omitempty"`
}

type MentionCandidate struct {
	ID, DisplayName, InsertText, ReferenceMode string
	URI                                        string
	// LegacyID can resolve an older draft as plain text only.
	LegacyID string
}
type MentionCandidates struct {
	ConversationID, AccountID ID
	State                     string
	Items                     []MentionCandidate
	Truncated                 bool
}
