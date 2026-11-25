package xmpp

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stanza"
)

const maxOMEMORoomMembers = 64

type roomOMEMOAuthority struct {
	slot                 *roomSlot
	revision, membership uint64
}

func (m *Manager) RoomEncryption(a domain.Account, room string) (bool, error) {
	if !m.OMEMOEnabled() {
		return false, nil
	}
	device, err := m.accountDevice(a.ID)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return device.RoomEncryption(room)
}

func (m *Manager) roomOMEMOEntry(id domain.ID) (*entry, *mx.Session) {
	m.mu.RLock()
	e := m.entries[id]
	m.mu.RUnlock()
	if e == nil {
		return nil, nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e, e.session
}

// The caller holds e.mu. Authorization is scoped to the current joined session.
func roomOMEMOAuthorityLocked(e *entry, s *mx.Session, room string) (roomOMEMOAuthority, bool) {
	slot := e.rooms[room]
	if s == nil || e.session != s || e.state.State != "connected" || slot == nil ||
		slot.session != s || slot.snapshot.State != "joined" || slot.leaving ||
		!omemoAffiliation(slot.management.self.affiliation) {
		return roomOMEMOAuthority{}, false
	}
	config := currentRoomConfiguration(e, room)
	if config.pending || !config.omemo {
		return roomOMEMOAuthority{}, false
	}
	return roomOMEMOAuthority{slot, config.revision, slot.omemoMembershipRevision}, true
}

func omemoAffiliation(value string) bool {
	return value == "member" || value == "admin" || value == "owner"
}

func (m *Manager) discoverRoomOMEMO(ctx context.Context, e *entry, s *mx.Session, room string) (roomOMEMOAuthority, error) {
	if s == nil {
		return roomOMEMOAuthority{}, application.ErrStateConflict
	}
	revision := currentRoomConfiguration(e, room).revision
	info, err := m.queryInfo(ctx, e, s, room)
	if err != nil {
		return roomOMEMOAuthority{}, err
	}
	if err := m.recordDiscoInfo(ctx, e, s, room, revision, info); err != nil {
		return roomOMEMOAuthority{}, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	authority, ok := roomOMEMOAuthorityLocked(e, s, room)
	if !ok {
		return roomOMEMOAuthority{}, application.ErrStateConflict
	}
	return authority, nil
}

func (m *Manager) roomOMEMOMembers(ctx context.Context, e *entry, s *mx.Session, room string) ([]string, error) {
	var members []string
	for _, affiliation := range []string{"owner", "admin", "member"} {
		addresses, err := m.readRoomAffiliations(ctx, e, s, room, affiliation)
		if err != nil {
			return nil, err
		}
		members = append(members, addresses...)
		if len(members) > maxOMEMORoomMembers {
			return nil, omemo.ErrInvalid
		}
	}
	slices.Sort(members)
	if !slices.Contains(members, e.account.Address) || slices.Contains(members, room) {
		return nil, omemo.ErrInvalid
	}
	return slices.Compact(members), nil
}

func (m *Manager) InspectRoomOMEMO(ctx context.Context, a domain.Account, room string) (application.RoomOMEMOState, error) {
	state := application.RoomOMEMOState{Address: room, Members: []string{}}
	var err error
	state.Enabled, err = m.RoomEncryption(a, room)
	if err != nil {
		return state, err
	}
	e, s := m.roomOMEMOEntry(a.ID)
	if s == nil {
		return state, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	authority, err := m.discoverRoomOMEMO(ctx, e, s, room)
	if err != nil {
		return state, nil
	}
	state.Eligible = true
	members, err := m.roomOMEMOMembers(ctx, e, s, room)
	e.mu.RLock()
	current, ok := roomOMEMOAuthorityLocked(e, s, room)
	e.mu.RUnlock()
	if err == nil && ok && current == authority {
		state.Members = members
	}
	return state, nil
}

func (m *Manager) SetRoomOMEMO(ctx context.Context, a domain.Account, room string, enabled bool) error {
	device, err := m.accountDevice(a.ID)
	if err != nil {
		return application.ErrProtocolUnavailable
	}
	if enabled {
		state, err := m.InspectRoomOMEMO(ctx, a, room)
		if err != nil {
			return err
		}
		if !state.Eligible || len(state.Members) == 0 {
			return application.ErrStateConflict
		}
	}
	if e, _ := m.roomOMEMOEntry(a.ID); e != nil {
		e.sendMu.Lock()
		defer e.sendMu.Unlock()
	}
	if err := m.saveRoomEncryption(device, roomOperationScope{a.ID, room}, enabled); err != nil {
		return err
	}
	m.WakeRoomArchive(a.ID)
	return nil
}

func (m *Manager) saveRoomEncryption(device *omemo.Device, scope roomOperationScope, enabled bool) error {
	m.plainRoomOperationsMu.Lock()
	defer m.plainRoomOperationsMu.Unlock()
	if enabled && m.plainRoomOperations[scope] != 0 {
		return application.ErrStateConflict
	}
	return device.SetRoomEncryption(scope.room, enabled)
}

func (m *Manager) prepareRoomOMEMO(ctx context.Context, e *entry, s *mx.Session, room string) (roomOMEMOAuthority, []string, []omemo.Target, error) {
	authority, err := m.discoverRoomOMEMO(ctx, e, s, room)
	if err != nil {
		return authority, nil, nil, err
	}
	members, err := m.roomOMEMOMembers(ctx, e, s, room)
	if err != nil {
		return authority, nil, nil, err
	}
	self, _, err := e.omemoDevice.LocalIdentity()
	if err != nil {
		return authority, nil, nil, err
	}
	var targets []omemo.Target
	for _, member := range members {
		skip := uint32(0)
		if member == e.account.Address {
			skip = self
		}
		items, err := observeDevices(ctx, e, s, member, skip)
		if err != nil {
			return authority, nil, nil, err
		}
		targets = append(targets, items...)
		if len(targets) > 256 {
			return authority, nil, nil, omemo.ErrInvalid
		}
	}
	return authority, members, targets, nil
}

func (m *Manager) receiveRoomOMEMO(ctx context.Context, e *entry, msg stanza.Message, meta replyMetadata, env omemo.Envelope, stamp, delayFrom string) error {
	if e.omemoDevice == nil || stamp != "" || delayFrom != "" || msg.From.Localpart() == "" || msg.From.Resourcepart() == "" ||
		msg.To.String() != "" && msg.To.Bare().String() != e.account.Address {
		return nil
	}
	room, nick := msg.From.Bare().String(), msg.From.Resourcepart()
	enabled, err := e.omemoDevice.RoomEncryption(room)
	if err != nil || !enabled {
		return err
	}
	e.mu.RLock()
	authority, ok := roomOMEMOAuthorityLocked(e, e.session, room)
	var sender string
	var stableID string
	if ok {
		e.historyMu.Lock()
		if e.sidScopes[room] {
			stableID = roomStanzaID(msg.From, meta.Stable)
		}
		e.historyMu.Unlock()
		if nick == authority.slot.snapshot.Nickname {
			sender = e.account.Address
		} else if occupant := authority.slot.management.occupants[nick]; omemoAffiliation(occupant.affiliation) {
			sender = occupant.address
		}
	}
	e.mu.RUnlock()
	if sender == "" {
		return nil
	}
	if sender == e.account.Address {
		self, _, err := e.omemoDevice.LocalIdentity()
		if err != nil {
			return err
		}
		if self == env.Header.From {
			origin := meta.originID()
			if len(meta.Origins) == 0 {
				origin = msg.ID
			}
			return m.bindRoomOMEMOEcho(ctx, e.account.ID, room, msg.From.String(), origin, env.ProviderID(), stableID, "")
		}
	}
	return m.acceptRoomOMEMO(ctx, e, omemo.Pending{Peer: sender, Room: room, Nickname: nick, StanzaID: stableID, At: time.Now().UTC(), Envelope: env})
}

func (m *Manager) acceptRoomOMEMO(ctx context.Context, e *entry, p omemo.Pending) error {
	enabled, err := e.omemoDevice.RoomEncryption(p.Room)
	if err != nil || !enabled {
		return err
	}
	e.mu.RLock()
	_, ok := roomOMEMOAuthorityLocked(e, e.session, p.Room)
	e.mu.RUnlock()
	if !ok {
		return nil
	}
	in := application.Incoming{AccountID: e.account.ID, Kind: "room", Address: p.Room, SenderAddress: p.Peer,
		SenderName: p.Nickname, ProviderID: p.Envelope.ProviderID(), CreatedAt: p.At, IsOwn: p.Peer == e.account.Address, Encrypted: true}
	if p.StanzaID != "" {
		in.References = []domain.MessageReference{{Scope: "room:" + p.Room, Author: p.Room + "/" + p.Nickname, ID: p.StanzaID}}
	}
	var saved error
	err = e.omemoDevice.Receive(ctx, p, func(text string) error {
		if !readOMEMOPayload(&in, text) {
			return nil
		}
		saved = m.host.Receive(ctx, in)
		return saved
	})
	if err == nil {
		m.refreshOMEMOPreKeys(e, p.Envelope)
		return nil
	}
	var storageErr *fs.PathError
	if saved != nil && !errors.Is(saved, application.ErrInvalid) || errors.As(err, &storageErr) {
		return err
	}
	if errors.Is(err, omemo.ErrUnverified) {
		if p.Digest != "" {
			return nil
		}
		if err := e.omemoDevice.HoldPending(p); err != nil {
			if errors.As(err, &storageErr) {
				return err
			}
			return nil
		}
		select {
		case e.omemoPeers <- p.Peer:
		default:
		}
		return nil
	}
	if p.Digest != "" {
		return e.omemoDevice.Discard(p)
	}
	return nil
}
