package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const (
	roomAdminQuery   = `<query xmlns="http://jabber.org/protocol/muc#admin">`
	roomOwnerQuery   = `<query xmlns="http://jabber.org/protocol/muc#owner"/>`
	roomModerationNS = "urn:xmpp:message-moderate:1"
)

type roomOccupant struct {
	id, nickname, address, role, affiliation string
}

type roomManagement struct {
	topic     *string
	self      roomOccupant
	occupants map[string]roomOccupant
	revision  string
	flags     roomManagementFlags
	busy      bool
}

type roomManagementFlags struct {
	open, membersOnly, changeSubject, allowInvites, moderate, avatar, nonAnonymous bool
}

func managementFlags(info disco.Info) roomManagementFlags {
	var flags roomManagementFlags
	for _, feature := range info.Features {
		switch feature.Var {
		case "muc_open":
			flags.open = true
		case "muc_membersonly":
			flags.membersOnly = true
		case roomModerationNS:
			flags.moderate = true
		case "vcard-temp":
			flags.avatar = true
		case "muc_nonanonymous":
			flags.nonAnonymous = true
		}
	}
	for _, form := range info.Form {
		types, _ := form.Raw("FORM_TYPE")
		if len(types) != 1 || types[0] != "http://jabber.org/protocol/muc#roominfo" {
			continue
		}
		for name, flag := range map[string]*bool{
			"muc#roomconfig_changesubject": &flags.changeSubject,
			"muc#roomconfig_allowinvites":  &flags.allowInvites,
		} {
			values, _ := form.Raw(name)
			*flag = len(values) == 1 && (values[0] == "1" || values[0] == "true")
		}
		break
	}
	return flags
}

// Call with e.mu held. The same nickname can be reused by a new occupant.
func observeRoomManagement(slot *roomSlot, p incomingPresence, self bool) {
	nick := p.From.Resourcepart()
	if p.MUC == nil || nick == "" || slot.leaving {
		return
	}
	state := &slot.management
	if self && p.Type == stanza.UnavailablePresence {
		*state = roomManagement{}
		return
	}
	if p.Type == stanza.UnavailablePresence {
		if _, exists := state.occupants[nick]; exists {
			delete(state.occupants, nick)
			state.revision = ""
		}
		return
	}
	if p.Type != "" {
		return
	}
	item := p.MUC.Item
	if !slices.Contains([]string{"moderator", "participant", "visitor"}, item.Role) ||
		!slices.Contains([]string{"owner", "admin", "member", "outcast", "none"}, item.Affiliation) {
		if self {
			state.self = roomOccupant{}
			state.revision = ""
		}
		if _, exists := state.occupants[nick]; exists {
			delete(state.occupants, nick)
			state.revision = ""
		}
		return
	}
	occupant := roomOccupant{nickname: nick, role: item.Role, affiliation: item.Affiliation}
	if address, err := jid.Parse(item.JID); err == nil && address.Localpart() != "" && !address.Bare().Equal(p.From.Bare()) {
		occupant.address = address.Bare().String()
	}
	if self {
		if state.self != occupant {
			state.self = occupant
			state.revision = ""
		}
		delete(state.occupants, nick)
		return
	}
	if nick == slot.snapshot.Nickname {
		return
	}
	if state.occupants == nil {
		state.occupants = map[string]roomOccupant{}
	}
	old, found := state.occupants[nick]
	if found && old.address == occupant.address {
		occupant.id = old.id
	} else {
		occupant.id = "occupant:" + randomQueryID()
	}
	if found || len(state.occupants) < 200 {
		if old != occupant {
			state.revision = ""
		}
		state.occupants[nick] = occupant
	}
}

func (s roomManagement) actions() []string {
	out := []string{}
	role, aff, flags := s.self.role, s.self.affiliation, s.flags
	if aff == "owner" {
		out = append(out, "rename")
		if flags.avatar {
			out = append(out, "set_avatar", "remove_avatar")
		}
	}
	if role == "moderator" || (role == "participant" || role == "visitor") && flags.changeSubject {
		out = append(out, "topic")
	}
	if s.canInvite() {
		out = append(out, "invite")
	}
	if role == "moderator" {
		out = append(out, "role")
		if flags.moderate {
			out = append(out, "moderate_message")
		}
	}
	if aff == "owner" || aff == "admin" {
		out = append(out, "affiliation")
	}
	return out
}

func (s roomManagement) canShowParticipantAvatars() bool {
	return s.flags.nonAnonymous || s.self.role == "moderator"
}

func (s roomManagement) canInvite() bool {
	role, aff, flags := s.self.role, s.self.affiliation, s.flags
	return role != "" && (flags.open || role == "moderator" || aff == "admin" || aff == "owner" ||
		flags.membersOnly && flags.allowInvites && aff == "member")
}

func (s roomManagement) allows(cmd domain.RoomManagementCommand) bool {
	actions := s.actions()
	if cmd.Action == "update_details" {
		return (cmd.Name == nil || slices.Contains(actions, "rename")) &&
			(cmd.Topic == nil || slices.Contains(actions, "topic"))
	}
	return slices.Contains(actions, cmd.Action)
}

func (s roomManagement) roles(target roomOccupant) []string {
	out := []string{}
	own := s.self
	if target.role == "moderator" {
		if target.affiliation != "owner" && target.affiliation != "admin" && (own.affiliation == "admin" || own.affiliation == "owner") {
			return append(out, "participant")
		}
		return out
	}
	if own.role == "moderator" {
		out = append(out, "none")
		if target.role == "visitor" {
			out = append(out, "participant")
		} else if target.affiliation == "none" || own.affiliation == "admin" || own.affiliation == "owner" {
			out = append(out, "visitor")
		}
	}
	if own.affiliation == "owner" || own.affiliation == "admin" {
		out = append(out, "moderator")
	}
	return out
}

func (s roomManagement) affiliations(target roomOccupant) []string {
	out := []string{}
	if s.self.affiliation == "owner" {
		out = append(out, "none", "member", "admin", "owner", "outcast")
	} else if s.self.affiliation == "admin" && target.affiliation != "owner" && target.affiliation != "admin" {
		out = append(out, "none", "member", "outcast")
	}
	return slices.DeleteFunc(out, func(value string) bool { return value == target.affiliation })
}

func activeManagementSlot(e *entry, c domain.Conversation) *roomSlot {
	slot := e.rooms[c.Address]
	if slot == nil || slot.conversation.ID != c.ID || slot.session != e.session || e.session == nil ||
		e.state.State != "connected" || slot.snapshot.State != "joined" || slot.leaving {
		return nil
	}
	return slot
}

func (m *Manager) RoomManagement(ctx context.Context, a domain.Account, c domain.Conversation, affiliation string) (domain.RoomManagement, error) {
	out := domain.RoomManagement{State: "unavailable", Actions: []string{}, Occupants: []domain.RoomManagedOccupant{}, Affiliations: []string{}}
	e, _, err := m.roomEntry(a, c)
	if err != nil || e == nil {
		return out, err
	}
	e.mu.RLock()
	slot := activeManagementSlot(e, c)
	e.mu.RUnlock()
	if slot == nil {
		out.State = "not_joined"
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	version := currentRoomConfiguration(e, c.Address).revision
	payload, lookupErr := m.readHistoryIQ(ctx, e, slot.session, c.Address, stanza.GetIQ, discoInfoQuery)
	var info disco.Info
	if lookupErr == nil {
		info, lookupErr = parseDiscoInfo(payload)
	}
	e.mu.Lock()
	if activeManagementSlot(e, c) != slot || slot.management.busy || currentRoomConfiguration(e, c.Address).revision != version {
		e.mu.Unlock()
		return out, nil
	}
	state := &slot.management
	flags := roomManagementFlags{}
	if lookupErr == nil {
		flags = managementFlags(info)
	}
	if flags != state.flags {
		state.flags, state.revision = flags, ""
	}
	if state.self.role == "" {
		e.mu.Unlock()
		return out, nil
	}
	if state.revision == "" {
		state.revision = randomQueryID()
	}
	out.State, out.Revision, out.Actions = "available", state.revision, state.actions()
	own := state.self
	out.Occupants = append(out.Occupants, domain.RoomManagedOccupant{
		ID: "self", Nickname: own.nickname, Role: own.role, Affiliation: own.affiliation,
		Self: true, Roles: []string{}, Affiliations: []string{},
	})
	addresses := map[string]string{"self": a.Address}
	for _, occupant := range state.occupants {
		item := domain.RoomManagedOccupant{
			ID: occupant.id, Nickname: occupant.nickname, Role: occupant.role, Affiliation: occupant.affiliation,
			Roles: state.roles(occupant), Affiliations: []string{},
		}
		if occupant.address != "" && occupant.address != a.Address {
			item.Affiliations = state.affiliations(occupant)
			if state.canShowParticipantAvatars() {
				addresses[item.ID] = occupant.address
			}
		}
		out.Occupants = append(out.Occupants, item)
	}
	e.avatarRoom = c.Address
	e.mu.Unlock()
	m.wakeAvatars(e)
	slices.SortFunc(out.Occupants, func(a, b domain.RoomManagedOccupant) int { return strings.Compare(a.Nickname, b.Nickname) })
	if host, ok := m.host.(application.AvatarHost); ok {
		for i := range out.Occupants {
			item := &out.Occupants[i]
			if address := addresses[item.ID]; address != "" {
				cached, _ := host.Avatar(ctx, a.ID, address)
				item.AvatarHash = cached.ImageHash
			}
		}
	}
	e.mu.RLock()
	current := activeManagementSlot(e, c) == slot && slot.management.revision == out.Revision
	e.mu.RUnlock()
	if !current {
		out.State, out.Revision = "unavailable", ""
		out.Actions, out.Occupants = []string{}, []domain.RoomManagedOccupant{}
		return out, nil
	}
	if affiliation != "" {
		if !slices.Contains(out.Actions, "affiliation") {
			return out, application.ErrStateConflict
		}
		out.Affiliations, err = m.readRoomAffiliations(ctx, e, slot.session, c.Address, affiliation)
		e.mu.RLock()
		current := activeManagementSlot(e, c) == slot && slot.management.revision == out.Revision
		e.mu.RUnlock()
		if !current || errors.Is(err, errDetailPermission) {
			return out, application.ErrStateConflict
		}
		if err != nil {
			return out, application.ErrProtocolUnavailable
		}
	}
	return out, nil
}

func (m *Manager) readRoomAffiliations(ctx context.Context, e *entry, session *mx.Session, room, affiliation string) ([]string, error) {
	payload, err := m.readHistoryIQ(ctx, e, session, room, stanza.GetIQ, roomAdminQuery+`<item affiliation="`+escaped(affiliation)+`"/></query>`)
	if err != nil {
		return nil, err
	}
	var response struct {
		Query *struct {
			Items []struct {
				Address     string `xml:"jid,attr"`
				Affiliation string `xml:"affiliation,attr"`
			} `xml:"http://jabber.org/protocol/muc#admin item"`
		} `xml:"http://jabber.org/protocol/muc#admin query"`
	}
	if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &response) != nil || response.Query == nil {
		return nil, errArchive
	}
	out := []string{}
	for _, item := range response.Query.Items {
		if target, err := jid.Parse(item.Address); err == nil && target.Localpart() != "" && target.Resourcepart() == "" && item.Affiliation == affiliation {
			out = append(out, target.String())
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func (s roomManagement) payload(a domain.Account, c domain.Conversation, cmd domain.RoomManagementCommand, message domain.Message) (string, string) {
	if !s.allows(cmd) {
		return "", "denied"
	}
	target, reason := cmd.Target, ""
	if cmd.Reason != "" {
		reason = "<reason>" + escaped(cmd.Reason) + "</reason>"
	}
	switch cmd.Action {
	case "topic":
		return "<subject>" + escaped(cmd.Value) + "</subject>", ""
	case "invite":
		return `<x xmlns="http://jabber.org/protocol/muc#user"><invite to="` + escaped(target) + `">` + reason + "</invite></x>", ""
	case "role", "affiliation":
		var occupant roomOccupant
		if strings.HasPrefix(target, "occupant:") {
			for _, observed := range s.occupants {
				if observed.id == target {
					occupant = observed
					break
				}
			}
			if occupant.id == "" {
				return "", "stale"
			}
		}
		if cmd.Action == "role" {
			if !slices.Contains(s.roles(occupant), cmd.Value) {
				return "", "denied"
			}
			return roomAdminQuery + `<item nick="` + escaped(occupant.nickname) + `" role="` + escaped(cmd.Value) + `">` + reason + "</item></query>", ""
		}
		if occupant.id != "" {
			target = occupant.address
		}
		if target == "" || target == a.Address || !slices.Contains(s.affiliations(occupant), cmd.Value) {
			return "", "denied"
		}
		return roomAdminQuery + `<item jid="` + escaped(target) + `" affiliation="` + escaped(cmd.Value) + `">` + reason + "</item></query>", ""
	case "moderate_message":
		if message.Moderation != nil {
			return "", "stale"
		}
		for _, ref := range message.References {
			if ref.Scope == "room:"+c.Address && usableID(ref.ID) {
				return `<moderate xmlns="` + roomModerationNS + `" id="` + escaped(ref.ID) + `"><retract xmlns="` + roomRetractionNS + `"/>` + reason + "</moderate>", ""
			}
		}
		return "", "unsupported"
	}
	return "", ""
}

func (m *Manager) ManageRoom(ctx context.Context, a domain.Account, c domain.Conversation, intent domain.ID, cmd domain.RoomManagementCommand, message domain.Message) string {
	e, room, err := m.roomEntry(a, c)
	if err != nil || e == nil {
		return "unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	release, err := m.acquireArchive(ctx, c.Address, archiveMaintenance)
	if err != nil {
		return "unavailable"
	}
	defer release()
	e.mu.RLock()
	slot := activeManagementSlot(e, c)
	if slot == nil || slot.management.busy || slot.management.revision != cmd.ExpectedRevision || slot.snapshot.IntentRevisionID != intent {
		e.mu.RUnlock()
		return "stale"
	}
	payload, outcome := slot.management.payload(a, c, cmd, message)
	session, raw := e.session, e.raw
	e.mu.RUnlock()
	if outcome != "" {
		return outcome
	}
	name := cmd.Name
	if cmd.Action == "rename" {
		name = &cmd.Value
	}
	if name != nil {
		form, err := readArchiveIQ(ctx, e, session, c.Address, stanza.GetIQ, roomOwnerQuery)
		if err != nil {
			return roomIQOutcome(err, false)
		}
		payload, err = roomConfigSubmit(form, *name, false)
		if err != nil {
			return "unsupported"
		}
	}
	avatarAction := cmd.Action == "set_avatar" || cmd.Action == "remove_avatar"
	if avatarAction {
		card, err := readArchiveIQ(ctx, e, session, c.Address, stanza.GetIQ, roomVCardQuery)
		if err != nil && !errors.Is(err, errArchiveCursor) {
			return roomIQOutcome(err, false)
		}
		payload, err = updateRoomVCard(card, cmd.Image)
		if err != nil {
			return "unsupported"
		}
	}
	messageAction := cmd.Action == "topic" || cmd.Action == "invite"
	if messageAction {
		e.sendMu.Lock()
		defer e.sendMu.Unlock()
	}
	e.mu.Lock()
	if activeManagementSlot(e, c) != slot || slot.management.busy || slot.management.revision != cmd.ExpectedRevision || slot.snapshot.IntentRevisionID != intent ||
		!m.roomEntryCurrent(ctx, c.ID, intent, false) {
		e.mu.Unlock()
		return "stale"
	}
	// Recheck occupant identity after waiting for the provider and the IQ lane.
	if !slot.management.allows(cmd) {
		outcome = "denied"
	} else if name == nil && !avatarAction {
		payload, outcome = slot.management.payload(a, c, cmd, message)
	}
	if outcome != "" || ctx.Err() != nil {
		e.mu.Unlock()
		if outcome != "" {
			return outcome
		}
		return "unavailable"
	}
	slot.management.revision = ""
	slot.management.busy = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		slot.management.busy = false
		e.mu.Unlock()
	}()
	if messageAction {
		kind := stanza.GroupChatMessage
		if cmd.Action == "invite" {
			kind = stanza.NormalMessage
		}
		return sendRoomManagementMessage(ctx, session, raw, room, kind, payload)
	}
	if avatarAction {
		_, err := readArchiveIQ(ctx, e, session, c.Address, stanza.SetIQ, payload)
		if err == nil {
			m.queueAvatar(e, room, "", false)
		}
		return roomIQOutcome(err, true)
	}
	if name != nil || cmd.Action != "update_details" {
		_, err = readArchiveIQ(ctx, e, session, c.Address, stanza.SetIQ, payload)
		if err != nil || name == nil {
			return roomIQOutcome(err, true)
		}
		e.mu.Lock()
		if activeManagementSlot(e, c) == slot {
			advanceRoomPresenceLocked(e, c.Address)
			queueRoomDetailsLocked(e, c)
		}
		e.mu.Unlock()
	}
	if cmd.Topic == nil {
		return "confirmed"
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	e.mu.RLock()
	current := activeManagementSlot(e, c) == slot && slot.snapshot.IntentRevisionID == intent &&
		slot.management.allows(domain.RoomManagementCommand{Action: "topic"}) && m.roomEntryCurrent(ctx, c.ID, intent, false)
	e.mu.RUnlock()
	if current && ctx.Err() == nil {
		outcome = sendRoomManagementMessage(ctx, session, raw, room, stanza.GroupChatMessage, "<subject>"+escaped(*cmd.Topic)+"</subject>")
	} else {
		outcome = "stale"
	}
	if outcome != "submitted" && name != nil {
		return "name_confirmed"
	}
	return outcome
}

func sendRoomManagementMessage(ctx context.Context, session *mx.Session, raw net.Conn, room jid.JID, kind stanza.MessageType, payload string) string {
	if raw == nil {
		return "unavailable"
	}
	deadline, _ := ctx.Deadline()
	_ = raw.SetWriteDeadline(deadline)
	err := session.Send(ctx, stanza.Message{To: room, Type: kind, ID: randomQueryID()}.Wrap(xml.NewDecoder(strings.NewReader(payload))))
	_ = raw.SetWriteDeadline(time.Time{})
	if err != nil {
		_ = raw.Close()
		return "unknown"
	}
	return "submitted"
}

func roomIQOutcome(err error, attempted bool) string {
	if err == nil {
		return "confirmed"
	}
	if errors.Is(err, errDetailPermission) {
		return "denied"
	}
	var rejected archiveIQRejected
	if errors.As(err, &rejected) || !attempted {
		return "unavailable"
	}
	return "unknown"
}

var _ application.RoomManagementProtocol = (*Manager)(nil)

// The room forwards the subject without a body to each joined occupant.
func (m *Manager) observeRoomTopic(e *entry, msg stanza.Message, topic string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	address := msg.From.Bare().String()
	slot := e.rooms[address]
	if slot == nil || activeManagementSlot(e, slot.conversation) != slot {
		return
	}
	topic = cleanEvidence(topic, 2000)
	slot.management.topic = &topic
	if detail := e.details[slot.conversation.ID]; detail != nil {
		detail.info.Topic = topic
	}
	queueRoomDetailsLocked(e, slot.conversation)
}
