package xmpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const maxRoomSessions = 256
const maxOwnRoomMessages = 512

type roomSlot struct {
	conversation             domain.Conversation
	snapshot                 domain.RoomSession
	session                  *mx.Session
	requestID                string
	joinDeadline             time.Time
	leaving                  bool
	creationName             *string
	configuring              bool
	mentionOccupants         map[string]observedMention
	ownOccupantID            string
	pendingOwnOccupantID     string
	pendingOccupantRevision  uint64
	recentReactionsRecovered bool
}
type ownRoomMessage struct{ address, body string }
type roomSessionHost interface {
	RoomSessionChanged(context.Context, domain.RoomSession) error
}

type mucUser struct {
	Status []struct {
		Code int `xml:"code,attr"`
	} `xml:"http://jabber.org/protocol/muc#user status"`
	Item struct {
		Nickname    string `xml:"nick,attr"`
		JID         string `xml:"jid,attr"`
		Affiliation string `xml:"affiliation,attr"`
		Role        string `xml:"role,attr"`
	} `xml:"http://jabber.org/protocol/muc#user item"`
}

func roomAddress(a domain.Account, c domain.Conversation) (jid.JID, error) {
	if c.AccountID != a.ID || c.Kind != "room" {
		return jid.JID{}, application.ErrInvalid
	}
	room, err := jid.Parse(c.Address)
	if err != nil || room.Localpart() == "" || room.Domainpart() == "" || room.Resourcepart() != "" || room.Bare().String() != c.Address {
		return jid.JID{}, application.ErrInvalid
	}
	return room, nil
}
func (m *Manager) roomEntry(a domain.Account, c domain.Conversation) (*entry, jid.JID, error) {
	room, err := roomAddress(a, c)
	if err != nil {
		return nil, room, err
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	return e, room, nil
}
func roomSnapshot(c domain.Conversation, state, nickname, code string, revision ...domain.ID) domain.RoomSession {
	out := domain.RoomSession{AccountID: c.AccountID, ConversationID: c.ID, State: state, Nickname: nickname, LastErrorCode: code, UpdatedAt: time.Now().UTC()}
	if len(revision) > 0 {
		out.IntentRevisionID = revision[0]
	}
	return out
}
func (m *Manager) notifyRoom(ctx context.Context, e *entry, slot *roomSlot, snapshot domain.RoomSession) {
	host, ok := m.host.(roomSessionHost)
	if !ok {
		return
	}
	m.mu.RLock()
	current := m.entries[e.account.ID] == e
	m.mu.RUnlock()
	if !current {
		return
	}
	e.mu.RLock()
	same := e.rooms[slot.conversation.Address] == slot && slot.snapshot.UpdatedAt.Equal(snapshot.UpdatedAt)
	e.mu.RUnlock()
	if same {
		_ = host.RoomSessionChanged(ctx, snapshot)
	}
}
func (m *Manager) JoinRoom(ctx context.Context, a domain.Account, c domain.Conversation, nickname string, intentRevision domain.ID) (domain.RoomSession, error) {
	return m.joinRoom(ctx, a, c, nickname, intentRevision, nil)
}

func (m *Manager) CreateRoom(ctx context.Context, a domain.Account, c domain.Conversation, nickname, name string, intentRevision domain.ID) (domain.RoomSession, error) {
	return m.joinRoom(ctx, a, c, nickname, intentRevision, &name)
}

func (m *Manager) joinRoom(ctx context.Context, a domain.Account, c domain.Conversation, nickname string, intentRevision domain.ID, creationName *string) (domain.RoomSession, error) {
	e, room, err := m.roomEntry(a, c)
	if err != nil {
		return domain.RoomSession{}, err
	}
	if nickname == "" {
		nickname = defaultRoomNickname(a)
	}
	if !utf8.ValidString(nickname) || strings.TrimSpace(nickname) == "" || len([]rune(nickname)) > 100 || strings.ContainsAny(nickname, "\x00\r\n") {
		return domain.RoomSession{}, application.ErrInvalid
	}
	occupant, err := room.WithResource(nickname)
	if err != nil {
		return domain.RoomSession{}, application.ErrInvalid
	}
	nickname = occupant.Resourcepart()
	if e == nil {
		return roomSnapshot(c, "not_joined", nickname, "not_connected", intentRevision), nil
	}
	e.mu.Lock()
	existing := e.rooms[c.Address]
	session := e.session
	connected := e.state.State == "connected"
	if connected && session != nil && existing != nil && existing.session == session && existing.configuring {
		snapshot := existing.snapshot
		e.mu.Unlock()
		return snapshot, nil
	}
	if connected && session != nil && existing != nil && existing.snapshot.Nickname == nickname && existing.session == session && sameRoomEntry(existing, creationName, intentRevision) && (existing.snapshot.State == "joining" || existing.snapshot.State == "joined") {
		existing.snapshot.IntentRevisionID = intentRevision
		existing.snapshot.UpdatedAt = time.Now().UTC()
		snapshot := existing.snapshot
		e.mu.Unlock()
		m.notifyRoom(ctx, e, existing, snapshot)
		return snapshot, nil
	}
	e.mu.Unlock()
	if session == nil || !connected {
		return roomSnapshot(c, "not_joined", nickname, "not_connected", intentRevision), nil
	}
	code := m.roomEntryAvailability(ctx, e, session, room, creationName != nil)
	if code != "" {
		e.roomNotifyMu.Lock()
		defer e.roomNotifyMu.Unlock()
		e.mu.Lock()
		if e.session != session || e.state.State != "connected" {
			e.mu.Unlock()
			return roomSnapshot(c, "not_joined", nickname, "not_connected", intentRevision), nil
		}
		if e.rooms == nil {
			e.rooms = map[string]*roomSlot{}
		}
		if len(e.rooms) >= maxRoomSessions && e.rooms[c.Address] == nil {
			e.mu.Unlock()
			return domain.RoomSession{}, application.ErrProtocolUnavailable
		}
		if old := e.rooms[c.Address]; old != nil && old.session == session && old.configuring {
			snapshot := old.snapshot
			e.mu.Unlock()
			return snapshot, nil
		}
		snapshot := roomSnapshot(c, "failed", nickname, code, intentRevision)
		snapshot.Revoked = code == "forbidden" || creationName != nil
		slot := &roomSlot{conversation: c, snapshot: snapshot, session: session}
		if old := e.rooms[c.Address]; old != nil && old.snapshot.State == "joined" {
			advanceRoomPresenceLocked(e, c.Address)
			if err := m.setRoomReactionPrivacy(ctx, e, c.Address, "", false); err != nil {
				e.mu.Unlock()
				return domain.RoomSession{}, err
			}
		}
		e.rooms[c.Address] = slot
		e.mu.Unlock()
		m.notifyRoom(ctx, e, slot, snapshot)
		return snapshot, nil
	}
	e.roomNotifyMu.Lock()
	defer e.roomNotifyMu.Unlock()
	e.mu.Lock()
	s, raw := e.session, e.raw
	if s != session || raw == nil || e.state.State != "connected" {
		e.mu.Unlock()
		return roomSnapshot(c, "not_joined", nickname, "not_connected", intentRevision), nil
	}
	if e.rooms == nil {
		e.rooms = map[string]*roomSlot{}
	}
	old := e.rooms[c.Address]
	if old != nil && old.conversation.ID != c.ID {
		e.mu.Unlock()
		return domain.RoomSession{}, application.ErrInvalid
	}
	if old != nil && old.session == s && old.configuring {
		snapshot := old.snapshot
		e.mu.Unlock()
		return snapshot, nil
	}
	if old != nil && old.session == s && old.snapshot.Nickname == nickname && sameRoomEntry(old, creationName, intentRevision) && (old.snapshot.State == "joining" || old.snapshot.State == "joined") {
		old.snapshot.IntentRevisionID = intentRevision
		old.snapshot.UpdatedAt = time.Now().UTC()
		snapshot := old.snapshot
		e.mu.Unlock()
		m.notifyRoom(ctx, e, old, snapshot)
		return snapshot, nil
	}
	if old == nil && len(e.rooms) >= maxRoomSessions {
		e.mu.Unlock()
		return domain.RoomSession{}, application.ErrProtocolUnavailable
	}
	if old != nil && old.snapshot.State == "joined" {
		advanceRoomPresenceLocked(e, c.Address)
		if err := m.setRoomReactionPrivacy(ctx, e, c.Address, "", false); err != nil {
			e.mu.Unlock()
			return domain.RoomSession{}, err
		}
	}
	slot := &roomSlot{conversation: c, snapshot: roomSnapshot(c, "joining", nickname, "", intentRevision), session: s, requestID: randomQueryID(), joinDeadline: time.Now().Add(10 * time.Second), creationName: creationName}
	e.rooms[c.Address] = slot
	e.mu.Unlock()
	e.sendMu.Lock()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	_ = raw.SetWriteDeadline(deadline)
	err = s.Send(ctx, stanza.Presence{To: occupant, ID: slot.requestID}.Wrap(xml.NewDecoder(strings.NewReader(`<x xmlns="http://jabber.org/protocol/muc"><history maxstanzas="0"/></x>`))))
	_ = raw.SetWriteDeadline(time.Time{})
	e.sendMu.Unlock()
	if err != nil {
		_ = raw.Close()
		e.mu.Lock()
		if e.rooms[c.Address] == slot {
			slot.snapshot = roomSnapshot(c, "failed", nickname, "unknown", intentRevision)
			slot.snapshot.Revoked = creationName != nil
		}
		snapshot := slot.snapshot
		e.mu.Unlock()
		m.notifyRoom(ctx, e, slot, snapshot)
		return snapshot, nil
	}
	m.notifyRoom(ctx, e, slot, slot.snapshot)
	return slot.snapshot, nil
}
func (m *Manager) LeaveRoom(ctx context.Context, a domain.Account, c domain.Conversation, intentRevision domain.ID) (domain.RoomSession, error) {
	e, room, err := m.roomEntry(a, c)
	if err != nil {
		return domain.RoomSession{}, err
	}
	if e == nil {
		return roomSnapshot(c, "not_joined", "", "", intentRevision), nil
	}
	e.roomNotifyMu.Lock()
	defer e.roomNotifyMu.Unlock()
	e.mu.Lock()
	slot := e.rooms[c.Address]
	if slot == nil {
		e.mu.Unlock()
		return roomSnapshot(c, "not_joined", "", "", intentRevision), nil
	}
	if slot.conversation.ID != c.ID {
		e.mu.Unlock()
		return domain.RoomSession{}, application.ErrInvalid
	}
	s := e.session
	raw := e.raw
	nickname := slot.snapshot.Nickname
	wasJoined := slot.snapshot.State == "joined" || slot.snapshot.State == "joining"
	advanceRoomPresenceLocked(e, c.Address)
	if err := m.setRoomReactionPrivacy(ctx, e, c.Address, "", false); err != nil {
		e.mu.Unlock()
		return domain.RoomSession{}, err
	}
	slot.snapshot = roomSnapshot(c, "not_joined", nickname, "", intentRevision)
	slot.leaving = true
	slot.requestID = ""
	snapshot := slot.snapshot
	e.mu.Unlock()
	if s != nil && wasJoined {
		occupant, _ := room.WithResource(nickname)
		e.sendMu.Lock()
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(10 * time.Second)
		}
		if raw != nil {
			_ = raw.SetWriteDeadline(deadline)
		}
		err = s.Send(ctx, stanza.Presence{To: occupant, Type: stanza.UnavailablePresence}.Wrap(nil))
		if raw != nil {
			_ = raw.SetWriteDeadline(time.Time{})
		}
		e.sendMu.Unlock()
		if err != nil && raw != nil {
			_ = raw.Close()
		}
	}
	m.notifyRoom(ctx, e, slot, snapshot)
	return snapshot, nil
}
func (m *Manager) RoomSession(ctx context.Context, a domain.Account, c domain.Conversation) (domain.RoomSession, error) {
	e, _, err := m.roomEntry(a, c)
	if err != nil {
		return domain.RoomSession{}, err
	}
	if e == nil {
		return roomSnapshot(c, "not_joined", "", "not_connected"), nil
	}
	e.roomNotifyMu.Lock()
	defer e.roomNotifyMu.Unlock()
	e.mu.Lock()
	slot := e.rooms[c.Address]
	if slot == nil {
		code := ""
		if e.session == nil || e.state.State != "connected" {
			code = "not_connected"
		}
		e.mu.Unlock()
		return roomSnapshot(c, "not_joined", "", code), nil
	}
	if slot.conversation.ID != c.ID {
		e.mu.Unlock()
		return domain.RoomSession{}, application.ErrInvalid
	}
	if slot.session != e.session || e.session == nil || e.state.State != "connected" {
		snapshot := roomSnapshot(c, "not_joined", slot.snapshot.Nickname, "not_connected", slot.snapshot.IntentRevisionID)
		e.mu.Unlock()
		return snapshot, nil
	}
	timedout := slot.snapshot.State == "joining" && time.Now().After(slot.joinDeadline)
	if timedout {
		slot.snapshot = roomSnapshot(c, "failed", slot.snapshot.Nickname, "timeout", slot.snapshot.IntentRevisionID)
		slot.snapshot.Revoked = slot.creationName != nil
	}
	snapshot := slot.snapshot
	e.mu.Unlock()
	if timedout {
		m.notifyRoom(ctx, e, slot, snapshot)
	}
	return snapshot, nil
}
func (m *Manager) captureRoomPresence(ctx context.Context, e *entry, p incomingPresence) (bool, error) {
	address := p.From.Bare().String()
	e.roomNotifyMu.Lock()
	defer e.roomNotifyMu.Unlock()
	e.mu.Lock()
	slot := e.rooms[address]
	if slot == nil || slot.session != e.session {
		e.mu.Unlock()
		return false, nil
	}
	self := false
	renamed := false
	created := false
	if p.MUC != nil {
		for _, status := range p.MUC.Status {
			self = self || status.Code == 110
			renamed = renamed || status.Code == 210
			created = created || status.Code == 201
		}
	}
	if p.From.Resourcepart() != slot.snapshot.Nickname {
		if self && renamed && slot.snapshot.State == "joining" && p.From.Resourcepart() != "" {
			slot.snapshot.Nickname = p.From.Resourcepart()
		} else {
			e.mu.Unlock()
			return true, nil
		}
	}
	if slot.snapshot.State == "joining" && p.ID != "" && slot.requestID != "" && p.ID != slot.requestID {
		e.mu.Unlock()
		return true, nil
	}
	// A room must send its self-join confirmation before live removal. While a
	// new join is pending, an old unavailable echo cannot revoke the new intent.
	if p.Type == stanza.UnavailablePresence && slot.snapshot.State == "joining" {
		e.mu.Unlock()
		return true, nil
	}
	if p.Type == stanza.ErrorPresence {
		if slot.snapshot.State != "joining" {
			e.mu.Unlock()
			return true, nil
		}
		code := "unknown"
		if p.Error != nil {
			switch {
			case p.Error.Conflict != nil:
				code = "nickname_conflict"
			case p.Error.Forbidden != nil:
				code = "forbidden"
			case p.Error.NotAuthorized != nil:
				code = "not_authorized"
			case p.Error.RegistrationRequired != nil:
				code = "registration_required"
			case p.Error.ItemNotFound != nil:
				code = "room_unavailable"
			}
		}
		slot.snapshot = roomSnapshot(slot.conversation, "failed", slot.snapshot.Nickname, code, slot.snapshot.IntentRevisionID)
		slot.snapshot.Revoked = slot.creationName != nil || code == "forbidden" || code == "not_authorized" || code == "registration_required"
	} else if self && p.Type == stanza.UnavailablePresence {
		code := ""
		if p.MUC != nil {
			for _, status := range p.MUC.Status {
				switch status.Code {
				case 301:
					code = "forbidden"
				case 307:
					code = "removed"
				case 321, 322:
					code = "removed"
				case 332:
					code = "room_unavailable"
				}
			}
		}
		state := "not_joined"
		if code != "" {
			state = "failed"
		}
		advanceRoomPresenceLocked(e, address)
		if err := m.setRoomReactionPrivacy(ctx, e, address, "", false); err != nil {
			e.mu.Unlock()
			return true, err
		}
		slot.ownOccupantID, slot.pendingOwnOccupantID = "", ""
		slot.snapshot = roomSnapshot(slot.conversation, state, slot.snapshot.Nickname, code, slot.snapshot.IntentRevisionID)
		slot.snapshot.Revoked = code == "forbidden" || code == "removed"
		slot.leaving = false
	} else if self && p.Type == "" && slot.snapshot.State == "joining" && !slot.leaving {
		if p.ID != "" && slot.requestID != "" && p.ID != slot.requestID {
			e.mu.Unlock()
			return true, nil
		}
		if created || slot.creationName != nil {
			if !slot.configuring {
				slot.configuring = true
				slot.joinDeadline = time.Now().Add(15 * time.Second)
				go m.finishRoomEntry(ctx, e, slot, p, created && p.MUC.Item.Affiliation == "owner")
			}
			e.mu.Unlock()
			return true, nil
		}
		configuration := advanceRoomPresenceLocked(e, address)
		captureOwnRoomOccupant(e, address, slot, p.Occupants)
		slot.snapshot = roomSnapshot(slot.conversation, "joined", slot.snapshot.Nickname, "", slot.snapshot.IntentRevisionID)
		if configuration.mode == application.RoomReactionsNonanonymous {
			queueRoomDetailsLocked(e, slot.conversation)
		}
	} else if self && p.Type == "" && slot.snapshot.State == "joined" && !slot.leaving {
		changed := captureOwnRoomOccupant(e, address, slot, p.Occupants)
		e.mu.Unlock()
		if changed {
			m.WakeRoomArchive(e.account.ID)
		}
		return true, nil
	} else {
		e.mu.Unlock()
		return true, nil
	}
	snapshot := slot.snapshot
	session := slot.session
	e.mu.Unlock()
	m.notifyRoom(ctx, e, slot, snapshot)
	if snapshot.State == "joined" && pendingRoomReactionReplay(e, session, address, true) != nil {
		m.WakeRoomArchive(e.account.ID)
	}
	return true, nil
}

// A scoped self-presence can precede the discovery reply. Hold its candidate
// for that configuration and only use it after the room proves support.
// The caller holds e.mu.
func captureOwnRoomOccupant(e *entry, room string, slot *roomSlot, ids []wireID) bool {
	id := verifiedOccupantID(ids)
	if id == "" {
		return false
	}
	e.historyMu.Lock()
	defer e.historyMu.Unlock()
	configuration := e.roomConfigurations[room]
	if configuration.pending {
		slot.pendingOwnOccupantID, slot.pendingOccupantRevision = id, configuration.revision
		return false
	}
	return e.occupantScopes[room] && updateOwnRoomOccupant(slot, id)
}

func updateOwnRoomOccupant(slot *roomSlot, id string) bool {
	if id == slot.ownOccupantID {
		return false
	}
	slot.ownOccupantID = id
	slot.recentReactionsRecovered = false
	return true
}

func (m *Manager) resetRoomSessions(ctx context.Context, e *entry, s *mx.Session) {
	e.roomNotifyMu.Lock()
	defer e.roomNotifyMu.Unlock()
	e.mu.Lock()
	snapshots := map[*roomSlot]domain.RoomSession{}
	for _, slot := range e.rooms {
		if slot.session == s {
			slot.snapshot = roomSnapshot(slot.conversation, "not_joined", slot.snapshot.Nickname, "not_connected", slot.snapshot.IntentRevisionID)
			slot.session = nil
			slot.requestID = ""
			snapshots[slot] = slot.snapshot
		}
	}
	e.mu.Unlock()
	for slot, snapshot := range snapshots {
		m.notifyRoom(ctx, e, slot, snapshot)
	}
}

func (m *Manager) receiveRoomMessage(ctx context.Context, e *entry, msg stanza.Message, meta replyMetadata, body, stamp, delayFrom string, files fileSharingMetadata, operations ...correctionMetadata) error {
	if strings.TrimSpace(body) == "" || msg.From.Localpart() == "" || msg.From.Resourcepart() == "" {
		return nil
	}
	address := msg.From.Bare().String()
	e.mu.RLock()
	slot := e.rooms[address]
	if slot == nil || slot.session != e.session || slot.snapshot.State != "joined" {
		e.mu.RUnlock()
		return nil
	}
	nickname := slot.snapshot.Nickname
	own := msg.From.Resourcepart() == nickname
	var canonical domain.ID
	if own && len(meta.Origins) == 1 && usableID(meta.Origins[0].ID) && meta.Origins[0].Text == "" && len(meta.Origins[0].Children) == 0 {
		origin := meta.Origins[0].ID
		if tracked, ok := e.ownRoomMessages[origin]; !ok || (tracked.address == address && tracked.body == body) {
			canonical, _ = domain.ParseID(origin)
		}
	}
	e.mu.RUnlock()
	e.historyMu.Lock()
	stable := e.sidScopes[address]
	occupantVerified := e.occupantScopes[address]
	e.historyMu.Unlock()
	refs, reply := decodeReplyMetadata(meta, body, msg.From, e.account.Address, address, true, stable)
	provider := roomProviderID(msg.From, msg.ID, refs)
	if provider == "" {
		provider = "live-room:" + randomQueryID()
	}

	if canonical != "" {
		provider = string(canonical)
	}
	created := time.Now().UTC()
	historical := false
	if delayed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
		created = delayed.UTC()
		// XEP-0045 room history is stamped by the room itself. A timestamp
		// alone also occurs on other delayed traffic and is not proof of history.
		from, parseErr := jid.Parse(delayFrom)
		historical = parseErr == nil && from.Resourcepart() == "" && from.Equal(msg.From.Bare())
	}
	var correction *domain.MessageCorrection
	if len(operations) == 1 {
		correction = decodeCorrectionMetadata(operations[0], msg, true, occupantVerified)
	}
	if own && canonical != "" {
		observeOwnCorrectionSource(e, string(canonical), correction, address, body)
	}
	err := m.host.Receive(ctx, application.Incoming{MediaThumbnails: decodeFileThumbnails(files), AccountID: e.account.ID, Address: address, Kind: "room", OwnRoomNickname: nickname, SenderName: msg.From.Resourcepart(), SenderAddress: liveRoomSenderAddress(e, msg.From, stamp, delayFrom), ProviderID: provider, OriginalText: body, CreatedAt: created, Historical: historical, IsOwn: own, References: refs, Reply: reply, CanonicalID: canonical, Correction: correction})
	if errors.Is(err, application.ErrInvalid) {
		return nil
	}
	return err
}

// Match the retained MAM identity when a client ID is present. IDs remain
// scoped to the full occupant; they are never used as room reply targets.
func roomProviderID(from jid.JID, id string, refs []domain.MessageReference) string {
	if usableID(id) {
		sum := sha256.Sum256([]byte(from.String() + "\x00" + id))
		return "room:" + hex.EncodeToString(sum[:])
	}
	for _, ref := range refs {
		if ref.Scope == "room:"+from.Bare().String() && usableID(ref.ID) {
			sum := sha256.Sum256([]byte(from.Bare().String() + "\x00" + ref.ID))
			return "room-sid:" + hex.EncodeToString(sum[:])
		}
	}
	return ""
}
