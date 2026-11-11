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
	room, public := recordRoomIdentity(e, address, info)
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
	if room || configuration.pending {
		if host, ok := m.host.(application.RoomReactionPrivacyHost); ok {
			if err := host.SetRoomReactionPrivacy(ctx, e.account.ID, address, public); err != nil {
				return err
			}
		}
	}
	recordReactionRestrictionsLocked(e, address, info)
	if configuration.pending {
		configuration.pending = false
		e.roomConfigurations[address] = configuration
	}
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
				detail.queued = true
				select {
				case e.detailsWake <- struct{}{}:
				default:
				}
			}
			var err error
			if host, ok := m.host.(application.RoomReactionPrivacyHost); ok {
				err = host.SetRoomReactionPrivacy(ctx, e.account.ID, room, false)
			}
			e.mu.Unlock()
			m.WakeRoomArchive(e.account.ID)
			return err
		}
	}
	return nil
}
