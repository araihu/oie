package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

var errDetailPermission = errors.New("conversation detail permission denied")

const maxDetailConversations = 256
const detailRefreshInterval = time.Minute

type detailSlot struct {
	conversation domain.Conversation
	info         domain.ConversationInfo
	lastAttempt  time.Time
	queued       bool
}

func cloneConversationInfo(info domain.ConversationInfo) domain.ConversationInfo {
	info.Features = slices.Clone(info.Features)
	info.Participants.Items = slices.Clone(info.Participants.Items)
	if info.OccupantCount != nil {
		n := *info.OccupantCount
		info.OccupantCount = &n
	}
	if info.PeerPresence != nil {
		p := *info.PeerPresence
		info.PeerPresence = &p
	}
	return info
}
func (m *Manager) ConversationInfo(_ context.Context, a domain.Account, c domain.Conversation) (domain.ConversationInfo, error) {
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	off := domain.EmptyConversationInfo(c.Kind, "not_connected")
	if e == nil || c.AccountID != a.ID {
		return off, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.State != "connected" {
		return off, nil
	}
	if e.details == nil {
		e.details = map[domain.ID]*detailSlot{}
	}
	slot := e.details[c.ID]
	if slot == nil {
		if len(e.details) >= maxDetailConversations {
			return domain.EmptyConversationInfo(c.Kind, "unavailable"), nil
		}
		slot = &detailSlot{conversation: domain.Conversation{ID: c.ID, AccountID: a.ID, Address: c.Address, Kind: c.Kind}, info: domain.EmptyConversationInfo(c.Kind, "pending")}
		e.details[c.ID] = slot
	}
	if slot.conversation.Address != c.Address || slot.conversation.Kind != c.Kind {
		return domain.EmptyConversationInfo(c.Kind, "unavailable"), nil
	}
	if c.Kind == "direct" {
		info := domain.EmptyConversationInfo(c.Kind, "known")
		presence := peerPresence(e, c.Address)
		info.PeerPresence = &presence
		return info, nil
	}
	if time.Since(slot.lastAttempt) >= detailRefreshInterval && !slot.queued {
		slot.lastAttempt = time.Now()
		slot.queued = true
		select {
		case e.detailsWake <- struct{}{}:
		default:
		}
	}
	info := cloneConversationInfo(slot.info)
	if room := e.rooms[c.Address]; room != nil && room.session == e.session {
		info.RoomSession = room.snapshot.State
	}
	return info, nil
}
func (m *Manager) detailsLoop(ctx context.Context, e *entry, s *mx.Session, wake <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
		for ctx.Err() == nil {
			e.mu.Lock()
			if e.session != s || e.state.State != "connected" {
				e.mu.Unlock()
				return
			}
			var selected *detailSlot
			var id domain.ID
			for key, slot := range e.details {
				if slot.queued {
					id = key
					selected = slot
					slot.queued = false
					break
				}
			}
			e.mu.Unlock()
			if selected == nil {
				break
			}
			info := m.fetchRoomDetails(ctx, e, s, selected.conversation.Address)
			e.mu.Lock()
			if e.session == s && e.state.State == "connected" && e.details[id] == selected {
				selected.info = info
			}
			e.mu.Unlock()
		}
	}
}
func detailFailure(err error) string {
	if errors.Is(err, errDetailPermission) {
		return "forbidden"
	}
	return "unavailable"
}
func (m *Manager) fetchRoomDetails(ctx context.Context, e *entry, s *mx.Session, address string) domain.ConversationInfo {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info := domain.EmptyConversationInfo("room", "known")
	raw, err := m.queryInfo(ctx, e, s, address)
	recordDiscovery(e, s, discovered("room", address, raw, err))
	if err != nil {
		info.State = detailFailure(err)
	} else {
		info = parseRoomInfo(raw)
	}
	payload, listErr := m.readHistoryIQ(ctx, e, s, address, stanza.GetIQ, `<query xmlns="http://jabber.org/protocol/disco#items"/>`)
	if listErr != nil {
		info.Participants.State = detailFailure(listErr)
	} else {
		info.Participants = parseOccupants(address, payload)
		if info.OccupantCount != nil && info.Participants.State == "known" && *info.OccupantCount != len(info.Participants.Items) {
			info.Participants.State = "partial"
		}
	}
	if (info.State == "known" && info.Participants.State != "known") || (info.State != "known" && info.Participants.State == "known") {
		info.State = "partial"
	}
	info.CheckedAt = time.Now().UTC()
	info.Participants.CheckedAt = info.CheckedAt
	return info
}
func parseRoomInfo(raw disco.Info) domain.ConversationInfo {
	info := domain.EmptyConversationInfo("room", "known")
	for _, identity := range raw.Identity {
		if identity.Category == "conference" && identity.Name != "" {
			info.Name = cleanEvidence(identity.Name, 300)
			break
		}
	}
	flags := map[string]string{"muc_public": "public", "muc_hidden": "hidden", "muc_membersonly": "members_only", "muc_open": "open", "muc_persistent": "persistent", "muc_temporary": "temporary", "muc_moderated": "moderated", "muc_unmoderated": "unmoderated", "muc_passwordprotected": "password_protected", "muc_unsecured": "unsecured", "muc_semianonymous": "anonymous", "muc_nonanonymous": "non_anonymous"}
	for _, feature := range raw.Features {
		if flag := flags[feature.Var]; flag != "" {
			info.Features = append(info.Features, flag)
		}
	}
	slices.Sort(info.Features)
	info.Features = slices.Compact(info.Features)
	for _, form := range raw.Form {
		values, ok := form.Raw("FORM_TYPE")
		if !ok || len(values) != 1 || values[0] != "http://jabber.org/protocol/muc#roominfo" {
			continue
		}
		field := func(key string, max int) string {
			values, ok := form.Raw(key)
			if !ok || len(values) != 1 {
				return ""
			}
			return cleanEvidence(values[0], max)
		}
		info.Description = field("muc#roominfo_description", 2000)
		info.Topic = field("muc#roominfo_subject", 2000)
		if n, err := strconv.ParseUint(field("muc#roominfo_occupants", 30), 10, 31); err == nil {
			count := int(n)
			info.OccupantCount = &count
		}
		break
	}
	return info
}
func parseOccupants(address, payload string) domain.ConversationParticipants {
	out := domain.ConversationParticipants{Scope: "occupants", State: "known", Items: []domain.ConversationParticipant{}}
	var raw struct {
		Query *struct {
			Items []struct {
				JID  string `xml:"jid,attr"`
				Name string `xml:"name,attr"`
				Node string `xml:"node,attr"`
			} `xml:"item"`
		} `xml:"http://jabber.org/protocol/disco#items query"`
	}
	if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &raw) != nil || raw.Query == nil {
		out.State = "unavailable"
		return out
	}
	room, err := jid.Parse(address)
	if err != nil {
		out.State = "unavailable"
		return out
	}
	seen := map[string]bool{}
	for _, item := range raw.Query.Items {
		occupant, err := jid.Parse(item.JID)
		if err != nil || len(item.JID) > 1024 || item.Node != "" || !occupant.Bare().Equal(room.Bare()) || occupant.Resourcepart() == "" {
			out.Truncated = true
			continue
		}
		key := occupant.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(out.Items) >= 200 {
			out.Truncated = true
			break
		}
		name := item.Name
		if name == "" {
			name = occupant.Resourcepart()
		}
		out.Items = append(out.Items, domain.ConversationParticipant{ID: key, DisplayName: cleanEvidence(name, 300), Source: "discovery", Presence: domain.PresenceObservation{State: "unknown"}})
	}
	if out.Truncated {
		out.State = "partial"
	}
	return out
}
