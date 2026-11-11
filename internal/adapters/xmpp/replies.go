package xmpp

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
)

const replyNS = "urn:xmpp:reply:0"
const fallbackNS = "urn:xmpp:fallback:0"
const sidNS = "urn:xmpp:sid:0"

type wireID struct {
	ID       string     `xml:"id,attr"`
	By       string     `xml:"by,attr"`
	Text     string     `xml:",chardata"`
	Children []struct{} `xml:",any"`
}
type wireReply struct {
	ID       string     `xml:"id,attr"`
	To       string     `xml:"to,attr"`
	Text     string     `xml:",chardata"`
	Children []struct{} `xml:",any"`
}
type wireFallback struct {
	For    string `xml:"for,attr"`
	Bodies []struct {
		Start *string `xml:"start,attr"`
		End   *string `xml:"end,attr"`
	} `xml:"urn:xmpp:fallback:0 body"`
	Subjects []struct{} `xml:"urn:xmpp:fallback:0 subject"`
	Others   []struct{} `xml:",any"`
}
type replyMetadata struct {
	Origins  []wireID       `xml:"urn:xmpp:sid:0 origin-id"`
	Stable   []wireID       `xml:"urn:xmpp:sid:0 stanza-id"`
	Replies  []wireReply    `xml:"urn:xmpp:reply:0 reply"`
	Fallback []wireFallback `xml:"urn:xmpp:fallback:0 fallback"`
}

func usableID(id string) bool {
	return id != "" && len(id) <= 4096 && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}

func roomStanzaID(from jid.JID, ids []wireID) string {
	var found *wireID
	for i := range ids {
		id := &ids[i]
		by, err := jid.Parse(id.By)
		if err != nil || by.Resourcepart() != "" || !by.Equal(from.Bare()) {
			continue
		}
		if found != nil {
			return ""
		}
		found = id
	}
	if found != nil && usableID(found.ID) && found.Text == "" && len(found.Children) == 0 {
		return found.ID
	}
	return ""
}

// Room IDs are accepted only after discovery confirms the room enforces SID
// assignment. Direct IDs remain scoped by the authenticated sender, never global.
func decodeReplyMetadata(meta replyMetadata, body string, from jid.JID, account, peer string, room, stableVerified bool) ([]domain.MessageReference, *domain.ReplyReference) {
	refs := []domain.MessageReference{}
	scope, author := "sender:"+from.Bare().String(), from.Bare().String()
	if room {
		scope, author = "room:"+from.Bare().String(), from.String()
	}
	if room && stableVerified {
		if id := roomStanzaID(from, meta.Stable); id != "" {
			refs = append(refs, domain.MessageReference{Scope: scope, ID: id, Author: author})
		}
	} else if !room {
		if len(meta.Origins) == 1 {
			id := meta.Origins[0]
			if usableID(id.ID) && id.Text == "" && len(id.Children) == 0 {
				refs = append(refs, domain.MessageReference{Scope: scope, ID: id.ID, Author: author})
			}
		}
	}
	if len(meta.Replies) != 1 {
		return refs, nil
	}
	reply := meta.Replies[0]
	if !usableID(reply.ID) || reply.Text != "" || len(reply.Children) != 0 {
		return refs, nil
	}
	target := domain.MessageReference{ID: reply.ID}
	if room {
		if !stableVerified {
			return refs, nil
		}
		target.Scope = "room:" + from.Bare().String()
		if reply.To != "" {
			to, err := jid.Parse(reply.To)
			if err != nil || !to.Bare().Equal(from.Bare()) {
				return refs, nil
			}
			if to.Resourcepart() != "" {
				target.Author = to.String()
			}
		}
	} else if reply.To != "" {
		to, err := jid.Parse(reply.To)
		if err != nil {
			return refs, nil
		}
		address := to.Bare().String()
		if address != account && address != peer {
			return refs, nil
		}
		target.Scope = "sender:" + address
		target.Author = address
	}
	result := &domain.ReplyReference{Target: target}
	fallbacks := []wireFallback{}
	for _, f := range meta.Fallback {
		if f.For == replyNS {
			fallbacks = append(fallbacks, f)
		}
	}
	if len(fallbacks) != 1 {
		return refs, result
	}
	f := fallbacks[0]
	count := utf8.RuneCountInString(body)
	if len(f.Bodies) == 0 && len(f.Subjects) == 0 && len(f.Others) == 0 {
		result.Fallback = []domain.TextRange{{Start: 0, End: count}}
		return refs, result
	}
	if len(f.Bodies) > 16 {
		return refs, result
	}
	for _, span := range f.Bodies {
		if span.Start == nil && span.End == nil {
			result.Fallback = []domain.TextRange{{Start: 0, End: count}}
			return refs, result
		}
		if span.Start == nil || span.End == nil {
			result.Fallback = nil
			return refs, result
		}
		start, e1 := strconv.Atoi(*span.Start)
		end, e2 := strconv.Atoi(*span.End)
		if e1 != nil || e2 != nil || start < 0 || end < start || end > count {
			result.Fallback = nil
			return refs, result
		}
		result.Fallback = append(result.Fallback, domain.TextRange{Start: start, End: end})
	}
	return refs, result
}

func (m *Manager) OutgoingReferences(a domain.Account, out application.Outgoing) []domain.MessageReference {
	if out.Kind == "room" {
		return nil
	}
	return []domain.MessageReference{{Scope: "sender:" + a.Address, ID: string(out.ID), Author: a.Address}}
}
