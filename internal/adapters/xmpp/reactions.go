package xmpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const reactionsNS = "urn:xmpp:reactions:0"

type reactionMetadata struct {
	Reactions []wireReactions `xml:"urn:xmpp:reactions:0 reactions"`
}

type wireReactions struct {
	ID       string         `xml:"id,attr"`
	Text     string         `xml:",chardata"`
	Items    []wireReaction `xml:"urn:xmpp:reactions:0 reaction"`
	Children []struct{}     `xml:",any"`
}

type wireReaction struct {
	Text     string     `xml:",chardata"`
	Children []struct{} `xml:",any"`
}

// A reaction payload is a complete actor set, including the empty removal set.
// The core performs emoji validation; the wire boundary limits allocation and
// rejects nested/multiple controls rather than treating their body as a message.
func reactionSet(meta reactionMetadata) (string, []string, bool) {
	if len(meta.Reactions) != 1 {
		return "", nil, false
	}
	r := meta.Reactions[0]
	if !usableID(r.ID) || strings.TrimSpace(r.Text) != "" || len(r.Children) != 0 || len(r.Items) > 32 {
		return "", nil, false
	}
	emojis := []string{}
	for _, item := range r.Items {
		if len(item.Children) != 0 || item.Text == "" || len(item.Text) > 256 || !utf8.ValidString(item.Text) || strings.IndexFunc(item.Text, unicode.IsControl) >= 0 {
			return "", nil, false
		}
		if !slices.Contains(emojis, item.Text) {
			emojis = append(emojis, item.Text)
		}
	}
	if _, err := application.NormalizeReactionEmojis(emojis); err != nil {
		return "", nil, false
	}
	return r.ID, emojis, true
}

func directReaction(a domain.Account, msg stanza.Message, meta reactionMetadata, at time.Time) (*application.IncomingReaction, bool) {
	if msg.Type != stanza.ChatMessage && msg.Type != stanza.NormalMessage || msg.From.Localpart() == "" || msg.From.Domainpart() == "" {
		return nil, false
	}
	target, emojis, ok := reactionSet(meta)
	if !ok {
		return nil, false
	}
	actor := msg.From.Bare().String()
	peer := actor
	own := actor == a.Address
	if own {
		peer = msg.To.Bare().String()
	} else if msg.To.String() != "" && msg.To.Bare().String() != a.Address {
		return nil, false
	}
	to, err := jid.Parse(peer)
	if err != nil || to.Localpart() == "" || to.Domainpart() == "" {
		return nil, false
	}
	return &application.IncomingReaction{AccountID: a.ID, Address: peer, Kind: "direct", ProviderID: msg.ID, CreatedAt: at.UTC(), IsOwn: own, Actor: actor, ActorName: actor, Target: domain.MessageReference{ID: target}, Emojis: emojis}, true
}

func (m *Manager) receiveReaction(ctx context.Context, e *entry, msg stanza.Message, meta reactionMetadata, stamp, delayFrom string) error {
	at := time.Now().UTC()
	if stamp != "" {
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil
		}
		at = parsed.UTC()
	} else if delayFrom != "" {
		return nil
	}
	if !usableID(msg.ID) {
		return nil
	}
	var in *application.IncomingReaction
	if msg.Type == stanza.GroupChatMessage {
		room := msg.From.Bare().String()
		if msg.From.Localpart() == "" || msg.From.Resourcepart() == "" {
			return nil
		}
		e.historyMu.Lock()
		stable := e.sidScopes[room]
		e.historyMu.Unlock()
		actor := liveRoomSenderAddress(e, msg.From, stamp, delayFrom)
		target, emojis, ok := reactionSet(meta)
		if !stable || actor == "" || !ok {
			return nil
		}
		in = &application.IncomingReaction{AccountID: e.account.ID, Address: room, Kind: "room", ProviderID: msg.ID, CreatedAt: at, Actor: actor, ActorName: msg.From.Resourcepart(), IsOwn: actor == e.account.Address, Target: domain.MessageReference{Scope: "room:" + room, ID: target}, Emojis: emojis}
	} else {
		var ok bool
		in, ok = directReaction(e.account, msg, meta, at)
		if !ok {
			return nil
		}
	}
	err := m.host.Receive(ctx, application.Incoming{AccountID: e.account.ID, Reaction: in})
	if errors.Is(err, application.ErrInvalid) || errors.Is(err, application.ErrNotFound) {
		return nil
	}
	return err
}

func archivedReaction(a domain.Account, scope string, r *archiveResult) (*application.IncomingReaction, bool, error) {
	msg := r.Forwarded.Message
	if msg == nil || scope != a.Address || msg.Type == stanza.GroupChatMessage {
		return nil, false, nil
	}
	if !usableID(r.ID) {
		return nil, false, errArchive
	}
	at, err := time.Parse(time.RFC3339Nano, r.Forwarded.Delay.Stamp)
	if err != nil {
		return nil, false, errArchive
	}
	in, ok := directReaction(a, msg.Message, msg.reactionMetadata, at)
	if !ok {
		return nil, false, nil
	}
	in.Historical, in.ArchiveScope, in.ArchiveID = true, scope, r.ID
	if !usableID(in.ProviderID) {
		sum := sha256.Sum256([]byte(scope + "\x00" + r.ID))
		in.ProviderID = "archive-reaction:" + hex.EncodeToString(sum[:])
	}
	return in, true, nil
}

type outboundReactions struct {
	XMLName xml.Name           `xml:"urn:xmpp:reactions:0 reactions"`
	ID      string             `xml:"id,attr"`
	Items   []outboundReaction `xml:"urn:xmpp:reactions:0 reaction"`
}
type outboundReaction struct {
	Text string `xml:",chardata"`
}
type storeHint struct {
	XMLName xml.Name `xml:"urn:xmpp:hints store"`
}
