package domain

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// WireMentionEvidence describes only whether the observed stanza carried a
// structured mention. Older room histories do not record its absence.
type WireMentionEvidence string

const (
	WireMentionsAbsent  WireMentionEvidence = "absent"
	WireMentionsPresent WireMentionEvidence = "present"
)

type WireMentionProof struct {
	Evidence WireMentionEvidence
	Digest   string
}

type WireMentionRange struct {
	URI        string
	Begin, End int
}

func WireMentionDigest(references []WireMentionRange) string {
	if len(references) == 0 {
		return ""
	}
	ordered := slices.Clone(references)
	slices.SortFunc(ordered, func(a, b WireMentionRange) int {
		return cmp.Or(cmp.Compare(a.Begin, b.Begin), cmp.Compare(a.End, b.End), strings.Compare(a.URI, b.URI))
	})
	canonical, _ := json.Marshal(ordered)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func (m Message) ConfirmsNoWireMentions(room bool) bool {
	return m.WireMentions == WireMentionsAbsent || (m.WireMentions == "" && !room)
}

func (m Message) MatchesWireMentions(observed WireMentionProof) bool {
	if m.WireMentions == WireMentionsAbsent {
		return observed.Evidence != WireMentionsPresent
	}
	if m.WireMentions == WireMentionsPresent {
		return observed.Evidence == WireMentionsPresent && observed.Digest != "" && m.WireMentionDigest == observed.Digest
	}
	offset := 0
	if m.OutboundReply != nil {
		offset = utf8.RuneCountInString(m.OriginalText) - utf8.RuneCountInString(m.OutboundReply.BodyText)
	}
	var expected []WireMentionRange
	for _, mention := range m.Mentions {
		if mention.ReferenceMode == "structured" {
			expected = append(expected, WireMentionRange{URI: mention.URI, Begin: mention.Begin + offset, End: mention.End + offset})
		}
	}
	if len(expected) == 0 {
		return observed.Evidence != WireMentionsPresent
	}
	// Redaction may have removed the offset of the originally transmitted text.
	return m.WireBodySHA256 == "" && observed.Evidence == WireMentionsPresent && observed.Digest != "" && WireMentionDigest(expected) == observed.Digest
}

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
