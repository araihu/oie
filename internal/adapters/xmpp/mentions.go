package xmpp

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const mentionNS = "urn:xmpp:reference:0"

type observedMention struct {
	present bool
	uri     string
}

// Only room-origin MUC presence from a room session already requested by this
// client can disclose a target. Nothing derives a personal JID from a nickname.
func captureRoomMentionPresence(e *entry, p incomingPresence) {
	if p.MUC == nil || p.From.Resourcepart() == "" || (p.Type != "" && p.Type != stanza.UnavailablePresence) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	slot := e.rooms[p.From.Bare().String()]
	if slot == nil || slot.session != e.session || e.state.State != "connected" || (slot.snapshot.State != "joined" && slot.snapshot.State != "joining") {
		return
	}
	if slot.mentionOccupants == nil {
		slot.mentionOccupants = map[string]observedMention{}
	}
	key := p.From.String()
	if _, ok := slot.mentionOccupants[key]; !ok && len(slot.mentionOccupants) >= 200 {
		return
	}
	value := observedMention{present: p.Type != stanza.UnavailablePresence}
	if value.present && p.MUC.Item.JID != "" {
		if target, err := jid.Parse(p.MUC.Item.JID); err == nil && target.Localpart() != "" && !target.Bare().Equal(p.From.Bare()) {
			value.uri = "xmpp:" + url.PathEscape(target.Bare().String())
		}
	}
	slot.mentionOccupants[key] = value
}
func candidateFromOccupant(room, key, name string, observation *observedMention) (domain.MentionCandidate, bool) {
	occupant, err := jid.Parse(key)
	if err != nil || occupant.Bare().String() != room || occupant.Resourcepart() == "" || len(key) > 1024 {
		return domain.MentionCandidate{}, false
	}
	nick := occupant.Resourcepart()
	if strings.IndexFunc(nick, unicode.IsControl) >= 0 || utf8.RuneCountInString(nick) > 1024 {
		return domain.MentionCandidate{}, false
	}
	if observation != nil && !observation.present {
		return domain.MentionCandidate{}, false
	}
	if name == "" {
		name = nick
	}
	out := domain.MentionCandidate{ID: key, DisplayName: cleanEvidence(name, 300), InsertText: "@" + nick, ReferenceMode: "text"}
	if observation != nil && observation.uri != "" {
		out.ReferenceMode, out.URI = "structured", observation.uri
	}
	return out, true
}
func (m *Manager) MentionCandidates(ctx context.Context, a domain.Account, c domain.Conversation) (domain.MentionCandidates, error) {
	out := domain.MentionCandidates{ConversationID: c.ID, AccountID: a.ID, State: "unavailable", Items: []domain.MentionCandidate{}}
	if c.Kind != "room" || c.AccountID != a.ID {
		return out, nil
	}
	info, err := m.ConversationInfo(ctx, a, c)
	if err != nil {
		return out, err
	}
	if info.State == "not_connected" {
		out.State = "not_connected"
		return out, nil
	}
	out.Truncated = info.Participants.Truncated
	if info.Participants.State == "known" || info.Participants.State == "partial" {
		out.State = info.Participants.State
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		out.State = "not_connected"
		return out, nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.state.State != "connected" {
		out.State = "not_connected"
		return out, nil
	}
	publicIdentities := slices.Contains(info.Features, "non_anonymous") && !slices.Contains(info.Features, "anonymous")
	observed := map[string]observedMention{}
	if slot := e.rooms[c.Address]; slot != nil && slot.session == e.session && (slot.snapshot.State == "joined" || slot.snapshot.State == "joining") {
		for key, value := range slot.mentionOccupants {
			if !publicIdentities {
				value.uri = ""
			}
			observed[key] = value
		}
	}
	byID := map[string]domain.MentionCandidate{}
	for _, p := range info.Participants.Items {
		var observation *observedMention
		if value, ok := observed[p.ID]; ok {
			observation = &value
		}
		if candidate, ok := candidateFromOccupant(c.Address, p.ID, p.DisplayName, observation); ok {
			byID[p.ID] = candidate
		}
	}
	for key, value := range observed {
		if candidate, ok := candidateFromOccupant(c.Address, key, "", &value); ok {
			byID[key] = candidate
		}
	}
	for _, candidate := range byID {
		out.Items = append(out.Items, candidate)
	}
	slices.SortFunc(out.Items, func(a, b domain.MentionCandidate) int { return strings.Compare(a.ID, b.ID) })
	if len(out.Items) > 200 {
		out.Items = out.Items[:200]
		out.Truncated = true
	}
	if len(observed) > 0 && out.State == "unavailable" {
		out.State = "partial"
	}
	if out.Truncated && out.State == "known" {
		out.State = "partial"
	}
	return out, nil
}

// Defensively validate the frozen adapter URI again at the wire boundary.
func mentionTargetURI(raw, room string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "xmpp" || u.Opaque == "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	value, err := url.PathUnescape(u.Opaque)
	if err != nil {
		return "", false
	}
	target, err := jid.Parse(value)
	if err != nil || target.Localpart() == "" || target.Resourcepart() != "" || target.String() == room {
		return "", false
	}
	return "xmpp:" + url.PathEscape(target.String()), true
}

var _ application.MentionProvider = (*Manager)(nil)
