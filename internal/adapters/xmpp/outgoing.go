package xmpp

import (
	"encoding/xml"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type outboundReplyElement struct {
	XMLName xml.Name `xml:"urn:xmpp:reply:0 reply"`
	ID      string   `xml:"id,attr"`
	To      string   `xml:"to,attr,omitempty"`
}
type outboundFallbackElement struct {
	XMLName xml.Name                `xml:"urn:xmpp:fallback:0 fallback"`
	For     string                  `xml:"for,attr"`
	Bodies  []outboundFallbackRange `xml:"urn:xmpp:fallback:0 body"`
}
type outboundFallbackRange struct {
	Start int `xml:"start,attr"`
	End   int `xml:"end,attr"`
}
type outboundMentionElement struct {
	XMLName xml.Name `xml:"urn:xmpp:reference:0 reference"`
	Type    string   `xml:"type,attr"`
	URI     string   `xml:"uri,attr"`
	Begin   int      `xml:"begin,attr"`
	End     int      `xml:"end,attr"`
}
type outboundMessage struct {
	stanza.Message
	Body   string `xml:"body"`
	Origin struct {
		XMLName xml.Name `xml:"urn:xmpp:sid:0 origin-id"`
		ID      string   `xml:"id,attr"`
	}
	Mentions []outboundMentionElement `xml:",omitempty"`
	Reply    *outboundReplyElement    `xml:",omitempty"`
	Fallback *outboundFallbackElement `xml:",omitempty"`
}

func (m *Manager) SelectReplyReference(a domain.Account, c domain.Conversation, target domain.Message) (domain.MessageReference, bool) {
	if c.AccountID != a.ID || target.AccountID != a.ID || target.ConversationID != c.ID {
		return domain.MessageReference{}, false
	}
	scope := "room:" + c.Address
	if c.Kind != "room" {
		scope = ""
	}
	candidates := []domain.MessageReference{}
	for _, ref := range target.References {
		if !usableID(ref.ID) {
			continue
		}
		if c.Kind == "room" {
			if ref.Scope != scope {
				continue
			}
			if ref.Author != "" {
				author, err := jid.Parse(ref.Author)
				if err != nil || author.Bare().String() != c.Address {
					continue
				}
			}
		} else {
			author := strings.TrimPrefix(ref.Scope, "sender:")
			if ref.Scope != "sender:"+author || (author != a.Address && author != c.Address) {
				continue
			}
			if ref.Author != "" {
				j, err := jid.Parse(ref.Author)
				if err != nil || j.Bare().String() != author {
					continue
				}
			}
		}
		if len(candidates) == 0 || candidates[0] != ref {
			candidates = append(candidates, ref)
		}
	}
	if len(candidates) != 1 {
		return domain.MessageReference{}, false
	}
	return candidates[0], true
}
func buildOutgoing(a domain.Account, out application.Outgoing) (outboundMessage, error) {
	to, err := jid.Parse(out.Address)
	if err != nil || to.Localpart() == "" || to.Resourcepart() != "" || to.Bare().String() != out.Address {
		return outboundMessage{}, application.ErrInvalid
	}
	kind := stanza.ChatMessage
	if out.Kind == "room" {
		kind = stanza.GroupChatMessage
	} else if out.Kind != "" && out.Kind != "direct" {
		return outboundMessage{}, application.ErrInvalid
	}
	payload := outboundMessage{Message: stanza.Message{To: to.Bare(), Type: kind, ID: string(out.ID)}, Body: out.OriginalText}
	payload.Origin.ID = string(out.ID)
	if out.Kind == "room" {
		total, last := utf8.RuneCountInString(out.OriginalText), 0
		if len(out.Mentions) > 32 {
			return outboundMessage{}, application.ErrInvalid
		}
		for _, mention := range out.Mentions {
			if mention.Begin < last || mention.End <= mention.Begin || mention.End > total {
				return outboundMessage{}, application.ErrInvalid
			}
			last = mention.End
			if mention.ReferenceMode != "structured" {
				continue
			}
			uri, ok := mentionTargetURI(mention.URI, out.Address)
			if !ok {
				return outboundMessage{}, application.ErrInvalid
			}
			payload.Mentions = append(payload.Mentions, outboundMentionElement{Type: "mention", URI: uri, Begin: mention.Begin, End: mention.End})
		}
	}
	ref := out.Reply
	if ref == nil || !usableID(ref.Target.ID) {
		return payload, nil
	}
	target := ref.Target
	address := ""
	if out.Kind == "room" {
		if target.Scope != "room:"+out.Address {
			return payload, nil
		}
		if target.Author != "" {
			author, err := jid.Parse(target.Author)
			if err != nil || !author.Bare().Equal(to.Bare()) {
				return payload, nil
			}
			address = author.String()
		}
	} else {
		address = strings.TrimPrefix(target.Scope, "sender:")
		if target.Scope != "sender:"+address || (address != out.Address && address != a.Address) {
			return payload, nil
		}
		if target.Author != "" {
			author, err := jid.Parse(target.Author)
			if err != nil || author.Bare().String() != address {
				return payload, nil
			}
			address = author.String()
		}
	}
	spans := []outboundFallbackRange{}
	end := 0
	total := utf8.RuneCountInString(out.OriginalText)
	if len(ref.Fallback) > 16 {
		return payload, nil
	}
	for _, span := range ref.Fallback {
		if span.Start < end || span.Start < 0 || span.End < span.Start || span.End > total {
			return payload, nil
		}
		spans = append(spans, outboundFallbackRange{Start: span.Start, End: span.End})
		end = span.End
	}
	payload.Reply = &outboundReplyElement{ID: target.ID, To: address}
	if len(spans) > 0 {
		payload.Fallback = &outboundFallbackElement{For: replyNS, Bodies: spans}
	}
	return payload, nil
}
