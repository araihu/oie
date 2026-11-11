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

func (m *Manager) receiveReaction(ctx context.Context, e *entry, msg stanza.Message, meta reactionMetadata, occupants []wireID, stamp, delayFrom string) error {
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
		if stamp != "" || delayFrom != "" {
			return nil
		}
		actor, own := incomingRoomReactionActor(e, msg.From, occupants)
		var ok bool
		in, ok = groupReaction(e.account.ID, room, msg, meta, actor, own, at)
		if !ok {
			return nil
		}
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

// Hash room and account scope with the server-issued identifier. The result is
// stable across nickname changes, and cannot be used as a contact address.
func roomReactionActor(account domain.ID, room, occupant string) string {
	if occupant == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(string(account) + "\x00" + room + "\x00" + occupant))
	return domain.RoomReactionActorPrefix + hex.EncodeToString(sum[:])
}

// The caller holds e.mu for a consistent room and connection snapshot.
func roomReactionSupport(e *entry, room string) (slot *roomSlot, semianonymous, occupantVerified bool) {
	slot = e.rooms[room]
	if e.state.State != "connected" || e.session == nil || slot == nil || slot.session != e.session || slot.snapshot.State != "joined" || slot.leaving {
		return nil, false, false
	}
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	if !e.sidScopes[room] {
		return nil, false, false
	}
	return slot, e.semianonymousRooms[room], e.occupantScopes[room]
}

func incomingRoomReactionActor(e *entry, from jid.JID, occupants []wireID) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	room := from.Bare().String()
	slot, semianonymous, occupantVerified := roomReactionSupport(e, room)
	if slot == nil {
		return "", false
	}
	if !semianonymous {
		actor := liveRoomSenderAddressLocked(e, from)
		return actor, actor == e.account.Address
	}
	if !occupantVerified || slot.ownOccupantID == "" {
		return "", false
	}
	occupant := verifiedOccupantID(occupants)
	return roomReactionActor(e.account.ID, room, occupant), occupant == slot.ownOccupantID
}

func groupReaction(account domain.ID, room string, msg stanza.Message, meta reactionMetadata, actor string, own bool, at time.Time) (*application.IncomingReaction, bool) {
	target, emojis, ok := reactionSet(meta)
	if !ok || actor == "" || msg.From.Localpart() == "" || msg.From.Resourcepart() == "" || msg.From.Bare().String() != room {
		return nil, false
	}
	return &application.IncomingReaction{AccountID: account, Address: room, Kind: "room", ProviderID: msg.ID, CreatedAt: at, Actor: actor, ActorName: msg.From.Resourcepart(), IsOwn: own, Target: domain.MessageReference{Scope: "room:" + room, ID: target}, Emojis: emojis}, true
}

func archivedReaction(a domain.Account, scope string, r *archiveResult, trust archiveTrust) (*application.IncomingReaction, bool, error) {
	msg := r.Forwarded.Message
	if msg == nil {
		return nil, false, nil
	}
	group := msg.Type == stanza.GroupChatMessage
	if group && (!trust.SIDVerified || !trust.OccupantVerified || trust.OwnReactionActor == "") || !group && scope != a.Address {
		return nil, false, nil
	}
	if !usableID(r.ID) {
		return nil, false, errArchive
	}
	at, err := time.Parse(time.RFC3339Nano, r.Forwarded.Delay.Stamp)
	if err != nil {
		return nil, false, errArchive
	}
	var in *application.IncomingReaction
	var ok bool
	if group {
		actor := roomReactionActor(a.ID, scope, verifiedOccupantID(msg.Occupants))
		in, ok = groupReaction(a.ID, scope, msg.Message, msg.reactionMetadata, actor, actor == trust.OwnReactionActor, at.UTC())
	} else {
		in, ok = directReaction(a, msg.Message, msg.reactionMetadata, at)
	}
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
