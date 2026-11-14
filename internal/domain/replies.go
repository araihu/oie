package domain

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// MessageReference is an adapter-validated identifier in a provider-defined
// authority scope. The application never interprets protocol addresses.
type MessageReference struct{ Scope, ID, Author string }
type TextRange struct{ Start, End int }
type ReplyReference struct {
	Target   MessageReference
	Fallback []TextRange
}
type MessageReply struct {
	MessageID                     ID
	Available                     bool
	SenderName, Excerpt, BodyText string
}

// SourceContextPageSize bounds the initial window for a shared-message jump.
const SourceContextPageSize = 16

type MessageContext struct {
	RequestedTargetID  ID
	TargetID           ID
	Items              []Message
	HasOlder, HasNewer bool
}

// ReplyBody uses Unicode code points without normalization. Invalid spans never
// hide text. Only protocol-marked fallback is moved into the quote projection.
func ReplyBody(body string, spans []TextRange) (string, string) {
	if len(spans) == 0 || len(spans) > 16 {
		return body, ""
	}
	chars := []rune(body)
	ranges := slices.Clone(spans)
	slices.SortFunc(ranges, func(a, b TextRange) int { return cmp.Compare(a.Start, b.Start) })
	end := 0
	var text, quote strings.Builder
	for _, span := range ranges {
		if span.Start < end || span.Start < 0 || span.End < span.Start || span.End > len(chars) {
			return body, ""
		}
		text.WriteString(string(chars[end:span.Start]))
		quote.WriteString(string(chars[span.Start:span.End]))
		end = span.End
	}
	text.WriteString(string(chars[end:]))
	lines := strings.Split(quote.String(), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, ">") {
			lines[i] = strings.TrimPrefix(strings.TrimPrefix(line, ">"), " ")
		}
	}
	return text.String(), strings.TrimSpace(strings.Join(lines, "\n"))
}

func DisplayBody(m Message) string {
	if m.Reply != nil {
		return m.Reply.BodyText
	}
	return m.OriginalText
}

func ReplyExcerpt(text string) string {
	remaining := 512
	for i := range text {
		if remaining == 0 {
			return text[:i]
		}
		remaining--
	}
	return text
}

func ReplyExcerptSHA256(excerpt string) string {
	sum := sha256.Sum256([]byte(excerpt))
	return hex.EncodeToString(sum[:])
}

func ValidReplyExcerptSHA256(digest string) bool {
	if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

// OutboundReply is the immutable, server-created send plan. It survives outbox
// retries without trusting later browser quotes or reselecting a provider ID.
type OutboundReply struct {
	TargetRevisionID              ID
	Reviewed                      bool   `json:",omitempty"`
	ExpectedExcerptSHA256         string `json:",omitempty"`
	TargetBodyHash                string
	TargetID                      ID
	SenderName, Excerpt, BodyText string
	Reference                     *MessageReference
	Fallback                      []TextRange
}

// ProjectOutboundReply restores authored body and frozen quote from durable
// payloads. Availability is established separately against retained source data.
func ProjectOutboundReply(m *Message) {
	if m.OutboundReply == nil {
		return
	}
	p := m.OutboundReply
	m.Reply = &MessageReply{MessageID: p.TargetID, SenderName: p.SenderName, Excerpt: p.Excerpt, BodyText: p.BodyText}
}
