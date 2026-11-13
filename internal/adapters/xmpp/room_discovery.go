package xmpp

import (
	"context"
	"encoding/xml"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const maxPublicRooms = 100

func (m *Manager) DiscoverRooms(ctx context.Context, a domain.Account, service string) (domain.RoomDirectory, error) {
	out := domain.RoomDirectory{State: "not_connected", Rooms: []domain.RoomDirectoryItem{}}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return out, nil
	}
	e.mu.RLock()
	session, connected := e.session, e.state.State == "connected"
	var addresses []string
	for _, service := range e.discovery.Services {
		if !serverServiceSupports(service, "http://jabber.org/protocol/muc") {
			continue
		}
		for _, identity := range service.Identities {
			if identity.Category == "conference" && identity.Type == "text" && service.Scope == "service" {
				addresses = append(addresses, service.Address)
				break
			}
		}
	}
	e.mu.RUnlock()
	if !connected || session == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if service != "" {
		value, err := jid.Parse(service)
		if err != nil || value.Localpart() != "" || value.Resourcepart() != "" {
			return out, application.ErrInvalid
		}
		info, err := m.queryInfo(ctx, e, session, value.String())
		if err != nil || !isTextRoom(info) {
			out.State = "unavailable"
			return out, nil
		}
		addresses = []string{value.String()}
	}
	found, partial := false, false
	seen := map[string]bool{}
	for _, address := range addresses {
		payload, err := m.readHistoryIQ(ctx, e, session, address, stanza.GetIQ,
			`<query xmlns="http://jabber.org/protocol/disco#items"><set xmlns="http://jabber.org/protocol/rsm"><max>100</max></set></query>`)
		if err != nil {
			partial = true
			continue
		}
		rooms, truncated, err := publicRoomItems(address, payload)
		if err != nil {
			partial = true
			continue
		}
		found, partial = true, partial || truncated
		for _, room := range rooms {
			if seen[room.Address] {
				continue
			}
			if len(out.Rooms) == maxPublicRooms {
				partial = true
				break
			}
			seen[room.Address] = true
			out.Rooms = append(out.Rooms, room)
		}
	}
	e.mu.RLock()
	current := e.session == session && e.state.State == "connected"
	e.mu.RUnlock()
	if !current {
		out.Rooms = []domain.RoomDirectoryItem{}
		return out, nil
	}
	out.State = "unavailable"
	if found {
		out.State = "known"
		if partial {
			out.State = "partial"
		}
	}
	slices.SortFunc(out.Rooms, func(a, b domain.RoomDirectoryItem) int { return strings.Compare(a.Address, b.Address) })
	return out, nil
}

func publicRoomItems(service, payload string) ([]domain.RoomDirectoryItem, bool, error) {
	var document struct {
		Query *struct {
			Items []struct {
				Address string `xml:"jid,attr"`
				Name    string `xml:"name,attr"`
				Node    string `xml:"node,attr"`
			} `xml:"http://jabber.org/protocol/disco#items item"`
			Set struct {
				Count int `xml:"http://jabber.org/protocol/rsm count"`
			} `xml:"http://jabber.org/protocol/rsm set"`
		} `xml:"http://jabber.org/protocol/disco#items query"`
	}
	if xml.Unmarshal([]byte("<root>"+payload+"</root>"), &document) != nil || document.Query == nil {
		return nil, false, errArchive
	}
	out := []domain.RoomDirectoryItem{}
	for _, item := range document.Query.Items {
		address, err := jid.Parse(item.Address)
		if err != nil || item.Node != "" || address.Domainpart() != service || address.Localpart() == "" || address.Resourcepart() != "" {
			continue
		}
		if len(out) == maxPublicRooms {
			return out, true, nil
		}
		out = append(out, domain.RoomDirectoryItem{Address: address.String(), Name: cleanEvidence(item.Name, 300)})
	}
	return out, document.Query.Set.Count > len(document.Query.Items), nil
}
