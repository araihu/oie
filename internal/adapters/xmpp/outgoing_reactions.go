package xmpp

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/form"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type reactionLimit struct {
	max     int
	allowed []string
}
type reactionDestination struct{ address, kind string }

func restrictions(info disco.Info) reactionLimit {
	limit := reactionLimit{max: 8}
	found := false
	for _, f := range info.Form {
		counts := map[string]int{}
		relevant, validType := false, false
		f.ForFields(func(field form.FieldData) {
			counts[field.Var]++
			if field.Var == "FORM_TYPE" && slices.Contains(field.Raw, reactionsNS+":restrictions") {
				relevant = true
				validType = field.Type == "hidden" && len(field.Raw) == 1
			}
		})
		if !relevant {
			continue
		}
		if found || !validType || counts["FORM_TYPE"] != 1 || counts["max_reactions_per_user"] > 1 || counts["allowlist"] > 1 {
			return reactionLimit{allowed: []string{}}
		}
		found = true
		if raw, ok := f.Raw("max_reactions_per_user"); ok {
			if len(raw) != 1 {
				return reactionLimit{allowed: []string{}}
			}
			max, err := strconv.Atoi(raw[0])
			if err != nil || max < 0 || max > 1024 {
				return reactionLimit{allowed: []string{}}
			}
			limit.max = min(8, max)
		}
		if raw, ok := f.Raw("allowlist"); ok {
			if len(raw) > 256 {
				return reactionLimit{allowed: []string{}}
			}
			limit.allowed = []string{}
			for _, emoji := range raw {
				if emoji == "" || len(emoji) > 256 || !utf8.ValidString(emoji) || strings.IndexFunc(emoji, unicode.IsControl) >= 0 {
					return reactionLimit{allowed: []string{}}
				}
				if !slices.Contains(limit.allowed, emoji) {
					limit.allowed = append(limit.allowed, emoji)
				}
			}
		}
	}
	return limit
}

func recordReactionRestrictions(e *entry, session *mx.Session, address string, info disco.Info) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != session || e.state.State != "connected" {
		return
	}
	if e.reactionLimits == nil {
		e.reactionLimits = map[string]reactionLimit{}
	}
	if _, exists := e.reactionLimits[address]; !exists && len(e.reactionLimits) >= maxRoomSessions+maxDiscoveredServices {
		return
	}
	e.reactionLimits[address] = restrictions(info)
}

func currentReactionLimit(e *entry, address string) reactionLimit {
	if limit, ok := e.reactionLimits[address]; ok {
		return limit
	}
	return reactionLimit{max: 8}
}

func (m *Manager) PrepareReaction(ctx context.Context, a domain.Account, c domain.Conversation, target domain.Message) (domain.OutgoingReaction, error) {
	if err := ctx.Err(); err != nil {
		return domain.OutgoingReaction{}, err
	}
	if a.Protocol != "xmpp" || c.AccountID != a.ID || target.AccountID != a.ID || target.ConversationID != c.ID {
		return domain.OutgoingReaction{}, application.ErrProtocolUnavailable
	}
	ref, ok := m.SelectReplyReference(a, c, target)
	if !ok {
		return domain.OutgoingReaction{}, application.ErrProtocolUnavailable
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return domain.OutgoingReaction{}, application.ErrProtocolUnavailable
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.session == nil || e.raw == nil || e.state.State != "connected" || e.correctionSession == "" {
		return domain.OutgoingReaction{}, application.ErrProtocolUnavailable
	}
	limit := currentReactionLimit(e, c.Address)
	plan := domain.OutgoingReaction{TargetMessageID: target.ID, Reference: ref, Actor: a.Address, SessionID: e.correctionSession, MaxEmojis: limit.max, AllowedEmojis: slices.Clone(limit.allowed)}
	if c.Kind == "room" {
		slot, actor := ownRoomReactionActor(e, c.Address)
		if actor == "" {
			return domain.OutgoingReaction{}, application.ErrProtocolUnavailable
		}
		if domain.IsRoomReactionActor(actor) {
			plan.RoomActor, plan.ActorName = actor, slot.snapshot.Nickname
		}
		plan.RoomIntentRevisionID = slot.snapshot.IntentRevisionID
	}
	return plan, nil
}

func ownRoomReactionActor(e *entry, room string) (*roomSlot, string) {
	slot, semianonymous, occupantVerified := roomReactionSupport(e, room)
	if slot == nil {
		return nil, ""
	}
	if semianonymous {
		if !occupantVerified {
			return nil, ""
		}
		return slot, roomReactionActor(e.account.ID, room, slot.ownOccupantID)
	}
	from, err := jid.Parse(room + "/" + slot.snapshot.Nickname)
	if err != nil || liveRoomSenderAddressLocked(e, from) != e.account.Address {
		return nil, ""
	}
	return slot, e.account.Address
}

func validReactionPayload(a domain.Account, out application.Outgoing) bool {
	p := out.Reaction
	if p == nil || p.Actor != a.Address || p.SessionID == "" || p.TargetMessageID == "" || out.ID == p.TargetMessageID || !usableID(p.Reference.ID) || out.OriginalText != "" || out.Correction != nil || out.Reply != nil || len(out.Mentions) != 0 || len(out.ReactionEmojis) > 8 {
		return false
	}
	if _, err := domain.ParseID(string(out.ID)); err != nil {
		return false
	}
	if _, err := domain.ParseID(string(p.TargetMessageID)); err != nil {
		return false
	}
	set := reactionMetadata{Reactions: []wireReactions{{ID: p.Reference.ID}}}
	for _, emoji := range out.ReactionEmojis {
		set.Reactions[0].Items = append(set.Reactions[0].Items, wireReaction{Text: emoji})
	}
	_, normalized, ok := reactionSet(set)
	if !ok || len(normalized) != len(out.ReactionEmojis) || !withinReactionLimit(out.ReactionEmojis, reactionLimit{max: p.MaxEmojis, allowed: p.AllowedEmojis}) {
		return false
	}
	if out.Kind == "room" {
		if p.RoomActor != "" && (!domain.IsRoomReactionActor(p.RoomActor) || strings.TrimSpace(p.ActorName) == "") {
			return false
		}
		if p.Reference.Author != "" {
			author, err := jid.Parse(p.Reference.Author)
			if err != nil || author.Bare().String() != out.Address {
				return false
			}
		}
		return p.Reference.Scope == "room:"+out.Address && p.RoomIntentRevisionID != "" && out.RoomIntentRevisionID == p.RoomIntentRevisionID
	}
	if out.Kind != "direct" && out.Kind != "" {
		return false
	}
	author := strings.TrimPrefix(p.Reference.Scope, "sender:")
	return p.RoomActor == "" && (author == a.Address || author == out.Address) && p.Reference.Scope == "sender:"+author && (p.Reference.Author == "" || p.Reference.Author == author) && p.RoomIntentRevisionID == ""
}

func withinReactionLimit(emojis []string, limit reactionLimit) bool {
	if limit.max < 0 || len(emojis) > limit.max {
		return false
	}
	if limit.allowed != nil {
		for _, emoji := range emojis {
			if !slices.Contains(limit.allowed, emoji) {
				return false
			}
		}
	}
	return true
}

// The send mutex is held by the caller. A frozen reaction never rebinds to a
// new authenticated connection or silently becomes an ordinary chat message.
func validFrozenReaction(e *entry, a domain.Account, out application.Outgoing) bool {
	if !validReactionPayload(a, out) || out.Reaction.SessionID != e.correctionSession || !withinReactionLimit(out.ReactionEmojis, currentReactionLimit(e, out.Address)) {
		return false
	}
	if out.Kind == "room" {
		slot, actor := ownRoomReactionActor(e, out.Address)
		if actor == "" || slot.snapshot.IntentRevisionID != out.Reaction.RoomIntentRevisionID {
			return false
		}
		if out.Reaction.RoomActor != "" {
			return actor == out.Reaction.RoomActor
		}
		return actor == a.Address
	}
	return true
}

func trackReactionCommand(e *entry, out application.Outgoing) {
	if e.reactionCommands == nil {
		e.reactionCommands = map[string]reactionDestination{}
	}
	if len(e.reactionCommands) >= maxOwnRoomMessages {
		for key := range e.reactionCommands {
			delete(e.reactionCommands, key)
			break
		}
	}
	e.reactionCommands[string(out.ID)] = reactionDestination{address: out.Address, kind: out.Kind}
}

func (m *Manager) rejectReaction(ctx context.Context, e *entry, msg stanza.Message) (bool, error) {
	e.mu.RLock()
	destination, tracked := e.reactionCommands[msg.ID]
	e.mu.RUnlock()
	if !tracked {
		return false, nil
	}
	fromServer := false
	peer, _ := jid.Parse(destination.address)
	own, _ := jid.Parse(e.account.Address)
	if msg.From.Bare().Equal(peer.Bare()) {
		if destination.kind == "room" && msg.From.Resourcepart() != "" {
			return true, nil
		}
	} else if msg.From.Equal(peer.Domain()) || msg.From.Equal(own.Domain()) {
		fromServer = true
	} else {
		return true, nil
	}
	return true, m.host.Reject(ctx, application.Rejected{AccountID: e.account.ID, MessageID: msg.ID, Address: destination.address, FromServer: fromServer, FromParticipant: false})
}

var _ application.ReactionProvider = (*Manager)(nil)
