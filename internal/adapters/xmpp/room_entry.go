package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func isTextRoom(info disco.Info) bool {
	for _, identity := range info.Identity {
		if identity.Category == "conference" && identity.Type == "text" {
			return true
		}
	}
	return false
}

func sameRoomEntry(slot *roomSlot, name *string, revision domain.ID) bool {
	if slot.creationName == nil && name == nil {
		return true
	}
	return slot.creationName != nil && name != nil && slot.snapshot.IntentRevisionID == revision
}

func (m *Manager) roomEntryAvailability(ctx context.Context, e *entry, session *mx.Session, room jid.JID, create bool) string {
	info, err := m.queryInfo(ctx, e, session, room.String())
	switch {
	case err == nil && create:
		return "room_exists"
	case errors.Is(err, errArchiveCursor) && create:
		info, err := m.queryInfo(ctx, e, session, room.Domain().String())
		if err == nil && isTextRoom(info) {
			return ""
		}
		return "creation_failed"
	case errors.Is(err, errDetailPermission):
		return "forbidden"
	case err != nil:
		return "room_unavailable"
	case !isTextRoom(info):
		return "unsupported"
	default:
		return ""
	}
}

func (m *Manager) PreviewRoom(ctx context.Context, a domain.Account, address string) (domain.RoomPreview, []byte, error) {
	e, _, err := m.roomEntry(a, domain.Conversation{AccountID: a.ID, Address: address, Kind: "room"})
	if err != nil {
		return domain.RoomPreview{}, nil, err
	}
	preview := domain.RoomPreview{State: "not_connected"}
	if e == nil {
		return preview, nil, nil
	}
	e.mu.RLock()
	session, connected := e.session, e.state.State == "connected"
	e.mu.RUnlock()
	if session == nil || !connected {
		return preview, nil, nil
	}
	// Preview discovery must not update any joined-room capability evidence.
	lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
	payload, err := m.readHistoryIQ(lookup, e, session, address, stanza.GetIQ, discoInfoQuery)
	cancel()
	if errors.Is(err, errArchiveCursor) {
		return domain.RoomPreview{State: "not_found"}, nil, nil
	}
	if err != nil {
		return domain.RoomPreview{State: "unavailable"}, nil, ctx.Err()
	}
	discovered, err := parseDiscoInfo(payload)
	if err != nil || !isTextRoom(discovered) {
		return domain.RoomPreview{State: "unavailable"}, nil, nil
	}
	info := parseRoomInfo(discovered)
	preview = domain.RoomPreview{State: "known", Name: info.Name, Description: info.Description, Topic: info.Topic, OccupantCount: info.OccupantCount}
	lookup, cancel = context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	payload, err = m.readHistoryIQ(lookup, e, session, address, stanza.GetIQ, `<vCard xmlns="vcard-temp"/>`)
	var photo []byte
	if err == nil {
		photo = applyRoomVCard(&preview, payload)
	}
	e.mu.RLock()
	current := e.session == session && e.state.State == "connected"
	e.mu.RUnlock()
	if !current {
		return domain.RoomPreview{State: "not_connected"}, nil, nil
	}
	return preview, photo, ctx.Err()
}

func applyRoomVCard(preview *domain.RoomPreview, payload string) []byte {
	var document struct {
		Cards []struct {
			Name        string `xml:"vcard-temp FN"`
			Description string `xml:"vcard-temp DESC"`
			Photo       struct {
				Data string `xml:"vcard-temp BINVAL"`
			} `xml:"vcard-temp PHOTO"`
		} `xml:"vcard-temp vCard"`
	}
	if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &document) != nil || len(document.Cards) != 1 {
		return nil
	}
	card := document.Cards[0]
	if name := cleanEvidence(strings.TrimSpace(card.Name), 300); name != "" {
		preview.Name = name
	}
	if description := cleanEvidence(strings.TrimSpace(card.Description), 2000); description != "" {
		preview.Description = description
	}
	photo, _, _ := decodeAvatar(card.Photo.Data, "")
	return photo
}

// Self-presence for a locked room arrives on the receive loop, which must
// remain free to receive the configuration IQ response.
func (m *Manager) finishRoomEntry(ctx context.Context, e *entry, slot *roomSlot, presence incomingPresence, ownsNewRoom bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	room := slot.conversation.Address
	defer func() {
		e.mu.Lock()
		slot.configuring = false
		e.mu.Unlock()
	}()
	e.mu.RLock()
	session, raw := slot.session, e.raw
	current := e.rooms[room] == slot && session == e.session && slot.snapshot.State == "joining"
	e.mu.RUnlock()
	if !current || session == nil || raw == nil {
		return
	}
	code := ""
	cancelLocked := ownsNewRoom
	switch {
	case ownsNewRoom && slot.creationName != nil:
		submitted, err := m.configureNewRoom(ctx, e, session, slot, *slot.creationName)
		cancelLocked = !submitted
		if err != nil {
			code = "creation_failed"
		}
	case slot.creationName != nil:
		code = "room_exists"
	default:
		code = "room_unavailable"
	}
	if code != "" {
		// Cancel a locked room only when this request received owner status 201.
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		e.mu.RLock()
		current := e.session == session && e.rooms[room] == slot && slot.snapshot.State == "joining"
		e.mu.RUnlock()
		if cancelLocked && current {
			_, _ = m.readHistoryIQ(cleanup, e, session, room, stanza.SetIQ, `<query xmlns="http://jabber.org/protocol/muc#owner"><x xmlns="jabber:x:data" type="cancel"/></query>`)
		}
		e.roomNotifyMu.Lock()
		e.mu.RLock()
		leave := e.session == session && e.rooms[room] == slot && slot.snapshot.State == "joining"
		e.mu.RUnlock()
		if leave && cleanup.Err() == nil {
			e.sendMu.Lock()
			deadline, _ := cleanup.Deadline()
			_ = raw.SetWriteDeadline(deadline)
			err := session.Send(cleanup, stanza.Presence{To: presence.From, Type: stanza.UnavailablePresence}.Wrap(nil))
			_ = raw.SetWriteDeadline(time.Time{})
			e.sendMu.Unlock()
			if err != nil {
				_ = raw.Close()
			}
		}
		e.roomNotifyMu.Unlock()
		done()
	}
	e.roomNotifyMu.Lock()
	defer e.roomNotifyMu.Unlock()
	e.mu.Lock()
	if e.rooms[room] != slot || e.session != session || slot.snapshot.State != "joining" {
		e.mu.Unlock()
		return
	}
	if code == "" {
		advanceRoomPresenceLocked(e, room)
		captureOwnRoomOccupant(e, room, slot, presence.Occupants)
		slot.creationName = nil
		slot.snapshot = roomSnapshot(slot.conversation, "joined", slot.snapshot.Nickname, "", slot.snapshot.IntentRevisionID)
		queueRoomDetailsLocked(e, slot.conversation)
	} else {
		slot.snapshot = roomSnapshot(slot.conversation, "failed", slot.snapshot.Nickname, code, slot.snapshot.IntentRevisionID)
		slot.snapshot.Revoked = true
	}
	snapshot := slot.snapshot
	e.mu.Unlock()
	m.notifyRoom(ctx, e, slot, snapshot)
}

type roomConfigField struct {
	Var    string   `xml:"var,attr"`
	Type   string   `xml:"type,attr,omitempty"`
	Values []string `xml:"value"`
}

type roomConfigForm struct {
	Type   string            `xml:"type,attr"`
	Fields []roomConfigField `xml:"field"`
}

func roomConfigSubmission(payload, name string) (string, error) {
	var document struct {
		Owner struct {
			Form roomConfigForm `xml:"jabber:x:data x"`
		} `xml:"http://jabber.org/protocol/muc#owner query"`
	}
	if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &document) != nil || document.Owner.Form.Type != "form" {
		return "", errArchive
	}
	var submit struct {
		XMLName xml.Name       `xml:"http://jabber.org/protocol/muc#owner query"`
		Form    roomConfigForm `xml:"jabber:x:data x"`
	}
	submit.Form.Type = "submit"
	formType, persistent, named := false, false, name == ""
	for _, field := range document.Owner.Form.Fields {
		switch field.Var {
		case "FORM_TYPE":
			formType = field.Type == "hidden" && len(field.Values) == 1 && field.Values[0] == "http://jabber.org/protocol/muc#roomconfig"
		case "muc#roomconfig_persistentroom":
			persistent = field.Type == "boolean"
			field.Values = []string{"1"}
		case "muc#roomconfig_roomname":
			if name != "" {
				named = true
				field.Values = []string{name}
			}
		}
		if field.Var == "" || field.Type == "fixed" {
			continue
		}
		field.Type = ""
		submit.Form.Fields = append(submit.Form.Fields, field)
	}
	if !formType || !persistent || !named {
		return "", errArchive
	}
	data, err := xml.Marshal(submit)
	return string(data), err
}

func (m *Manager) configureNewRoom(ctx context.Context, e *entry, s *mx.Session, slot *roomSlot, name string) (bool, error) {
	room := slot.conversation.Address
	payload, err := m.readHistoryIQ(ctx, e, s, room, stanza.GetIQ, `<query xmlns="http://jabber.org/protocol/muc#owner"/>`)
	if err != nil {
		return false, err
	}
	submit, err := roomConfigSubmission(payload, name)
	if err != nil {
		return false, err
	}
	e.mu.RLock()
	current := e.session == s && e.rooms[room] == slot && slot.snapshot.State == "joining"
	e.mu.RUnlock()
	if !current {
		return false, errArchive
	}
	_, err = m.readHistoryIQ(ctx, e, s, room, stanza.SetIQ, submit)
	return true, err
}

var _ application.RoomPreviewProvider = (*Manager)(nil)
var _ application.RoomCreator = (*Manager)(nil)
