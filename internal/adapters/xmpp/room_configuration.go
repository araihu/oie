package xmpp

import (
	"context"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/stanza"
)

type roomConfiguration struct {
	revision uint64
	pending  bool
	mode     string
}

func currentRoomConfiguration(e *entry, room string) roomConfiguration {
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	return e.roomConfigurations[room]
}

func (m *Manager) recordDiscoInfo(ctx context.Context, e *entry, s *mx.Session, address string, revision uint64, info disco.Info) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	configuration := e.roomConfigurations[address]
	if e.session != s || e.state.State != "connected" || configuration.revision != revision {
		return errArchive
	}
	room, mode := recordRoomIdentity(e, address, info)
	if room || configuration.pending {
		stable := false
		for _, feature := range info.Features {
			stable = stable || feature.Var == sidNS
		}
		if e.sidScopes == nil {
			e.sidScopes = make(map[string]bool)
		}
		e.sidScopes[address] = room && stable
	}
	if room || configuration.pending || configuration.mode != "" {
		slot := e.rooms[address]
		joined := slot != nil && slot.session == s && slot.snapshot.State == "joined" && !slot.leaving
		if err := m.setRoomReactionPrivacy(ctx, e, address, mode, joined); err != nil {
			return err
		}
		if slot != nil {
			switch {
			case !e.occupantScopes[address]:
				updateOwnRoomOccupant(slot, "")
			case joined && slot.pendingOwnOccupantID != "" && slot.pendingOccupantRevision == revision:
				if updateOwnRoomOccupant(slot, slot.pendingOwnOccupantID) {
					wakeRoomArchiveLocked(e)
				}
			}
			slot.pendingOwnOccupantID = ""
		}
		configuration.pending, configuration.mode = false, mode
		if e.roomConfigurations == nil {
			e.roomConfigurations = make(map[string]roomConfiguration)
		}
		e.roomConfigurations[address] = configuration
	}
	recordReactionRestrictionsLocked(e, address, info)
	return nil
}

// Only the room itself can revoke the discovery evidence for its current
// configuration. Requests already in flight cannot reinstate that evidence.
func (m *Manager) invalidateRoomConfiguration(ctx context.Context, e *entry, msg stanza.Message, muc *mucUser) error {
	if msg.Type != stanza.GroupChatMessage || msg.From.Localpart() == "" || msg.From.Resourcepart() != "" {
		return nil
	}
	for _, status := range muc.Status {
		switch status.Code {
		case 104, 170, 171, 172, 173, 174:
			room := msg.From.String()
			e.mu.Lock()
			slot := e.rooms[room]
			if e.session == nil || slot == nil || slot.session != e.session || (slot.snapshot.State != "joined" && slot.snapshot.State != "joining") {
				e.mu.Unlock()
				return nil
			}
			e.historyMu.Lock()
			if e.roomConfigurations == nil {
				e.roomConfigurations = make(map[string]roomConfiguration)
			}
			e.roomConfigurations[room] = roomConfiguration{revision: e.roomConfigurations[room].revision + 1, pending: true}
			delete(e.archiveCapabilities, room)
			delete(e.occupantScopes, room)
			delete(e.sidScopes, room)
			for _, q := range e.archives {
				if q.Scope == room {
					q.Err = errArchive
				}
			}
			e.historyMu.Unlock()
			delete(e.reactionLimits, room)
			if detail := e.details[slot.conversation.ID]; detail != nil {
				detail.info = domain.EmptyConversationInfo("room", "pending")
			}
			queueRoomDetailsLocked(e, slot.conversation)
			var mode string
			switch status.Code {
			case 172:
				mode = application.RoomReactionsNonanonymous
			case 173:
				mode = application.RoomReactionsSemianonymous
			case 174:
				mode = application.RoomReactionsAnonymous
			}
			err := m.setRoomReactionPrivacy(ctx, e, room, mode, false)
			e.mu.Unlock()
			m.WakeRoomArchive(e.account.ID)
			return err
		}
	}
	return nil
}

// The caller holds e.mu. An empty mode suspends address visibility while
// preserving the last verified identity mode in the repository.
func (m *Manager) setRoomReactionPrivacy(ctx context.Context, e *entry, room, mode string, joined bool) error {
	host, ok := m.host.(application.RoomReactionPrivacyHost)
	if !ok {
		return nil
	}
	return host.SetRoomReactionPrivacy(ctx, e.account.ID, room, application.RoomReactionPrivacy{
		Mode: mode, Public: joined && mode == application.RoomReactionsNonanonymous,
	})
}

// A discovery response from before a room presence transition cannot grant
// address visibility to the new presence.
func advanceRoomPresenceLocked(e *entry, room string) roomConfiguration {
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	configuration := e.roomConfigurations[room]
	configuration.revision++
	if e.roomConfigurations == nil {
		e.roomConfigurations = make(map[string]roomConfiguration)
	}
	e.roomConfigurations[room] = configuration
	return configuration
}

func queueRoomDetailsLocked(e *entry, c domain.Conversation) {
	detail := e.details[c.ID]
	if detail == nil {
		if len(e.details) >= maxDetailConversations {
			return
		}
		if e.details == nil {
			e.details = make(map[domain.ID]*detailSlot)
		}
		detail = &detailSlot{conversation: c, info: domain.EmptyConversationInfo("room", "pending")}
		e.details[c.ID] = detail
	}
	detail.queued = true
	select {
	case e.detailsWake <- struct{}{}:
	default:
	}
}
