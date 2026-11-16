package xmpp

import (
	"context"
	"encoding/xml"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	"mellium.im/xmpp/stanza"
	"strconv"
	"time"
)

type peerResource struct {
	observation domain.PresenceObservation
	priority    int
}
type incomingPresence struct {
	stanza.Presence
	Show      string   `xml:"jabber:client show"`
	Status    string   `xml:"jabber:client status"`
	Priority  string   `xml:"jabber:client priority"`
	MUC       *mucUser `xml:"http://jabber.org/protocol/muc#user x"`
	Occupants []wireID `xml:"urn:xmpp:occupant-id:0 occupant-id"`
	Error     *struct {
		Conflict             *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas conflict"`
		Forbidden            *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas forbidden"`
		NotAuthorized        *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas not-authorized"`
		RegistrationRequired *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas registration-required"`
		ItemNotFound         *struct{} `xml:"urn:ietf:params:xml:ns:xmpp-stanzas item-not-found"`
	} `xml:"jabber:client error"`
	Update *struct {
		Photo *string `xml:"photo"`
	} `xml:"vcard-temp:x:update x"`
}

// Store only traffic from known direct peers. Missing traffic never means offline.
func capturePeerPresence(e *entry, p incomingPresence) {
	if p.MUC != nil || p.From.Localpart() == "" || (p.Type != "" && p.Type != stanza.UnavailablePresence) {
		return
	}
	priority := 0
	if p.Priority != "" {
		value, err := strconv.Atoi(p.Priority)
		if err != nil || value < -128 || value > 127 {
			return
		}
		priority = value
	}
	address := p.From.Bare().String()
	e.avatarMu.Lock()
	known := e.avatarTargets[address] == "direct"
	e.avatarMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.State != "connected" {
		return
	}
	for _, slot := range e.details {
		known = known || (slot.conversation.Kind == "direct" && slot.conversation.Address == address)
	}
	// Initial presence can arrive before the first roster snapshot. Keep
	// bounded observations and project them only to subscribed contacts.
	if !known && e.contactSubscriptions != nil && !e.contactSubscriptions[address] {
		return
	}
	if e.peerPresence == nil {
		e.peerPresence = map[string]map[string]peerResource{}
	}
	resources := e.peerPresence[address]
	if resources == nil {
		if len(e.peerPresence) >= 256 {
			return
		}
		resources = map[string]peerResource{}
		e.peerPresence[address] = resources
	}
	resource := p.From.Resourcepart()
	if len(resources) >= 16 {
		if _, ok := resources[resource]; !ok {
			return
		}
	}
	state := "available"
	if p.Type == stanza.UnavailablePresence {
		state = "unavailable"
	} else {
		switch p.Show {
		case "away":
			state = "away"
		case "dnd":
			state = "busy"
		case "xa":
			state = "extended_away"
		case "chat":
			state = "chat"
		}
	}
	resources[resource] = peerResource{observation: domain.PresenceObservation{State: state, StatusText: cleanEvidence(p.Status, 300), ObservedAt: time.Now().UTC()}, priority: priority}
}

// Caller holds e.mu. Prefer an available resource; explicit unavailability of one
// resource must not hide another active device. Equal priorities use observation time.
func peerPresence(e *entry, address string) domain.PresenceObservation {
	result := domain.PresenceObservation{State: "unknown"}
	bestPriority := -129
	active := false
	for _, v := range e.peerPresence[address] {
		available := v.observation.State != "unavailable"
		if active && !available {
			continue
		}
		if (available && !active) || (available == active && (v.priority > bestPriority || (v.priority == bestPriority && v.observation.ObservedAt.After(result.ObservedAt)))) {
			result = v.observation
			bestPriority = v.priority
			active = available
		}
	}
	return result
}

func (m *Manager) configuredPresence(ctx context.Context, account domain.ID) string {
	if host, ok := m.host.(application.AccountPresenceHost); ok {
		preferences, err := host.AccountCommunicationPreferences(ctx, account)
		if err == nil {
			switch preferences.Presence {
			case "away", "dnd":
				return preferences.Presence
			}
		}
	}
	return ""
}

func presencePayload(show string) xml.TokenReader {
	caps := avatarCaps().TokenReader()
	if show == "" {
		return caps
	}
	name := xml.Name{Space: "jabber:client", Local: "show"}
	return xmlstream.MultiReader(
		xmlstream.Token(xml.StartElement{Name: name}),
		xmlstream.Token(xml.CharData(show)),
		xmlstream.Token(xml.EndElement{Name: name}),
		caps,
	)
}

func (m *Manager) RefreshAccountPresence(ctx context.Context, account domain.Account) {
	m.mu.RLock()
	e := m.entries[account.ID]
	m.mu.RUnlock()
	if e == nil {
		return
	}
	e.presenceMu.Lock()
	defer e.presenceMu.Unlock()
	e.mu.RLock()
	s, current := e.session, e.state.State == "connected"
	e.mu.RUnlock()
	if s == nil || !current {
		return
	}
	show := m.configuredPresence(ctx, account.ID)
	if e.presenceShow == show {
		return
	}
	if err := s.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(presencePayload(show))); err == nil {
		e.presenceShow = show
	}
}

func (m *Manager) ContactPresence(account domain.ID, address string) *domain.PresenceObservation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e := m.entries[account]
	if e == nil {
		return nil
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.state.State != "connected" || !e.contactSubscriptions[address] {
		return nil
	}
	observed := peerPresence(e, address)
	if observed.State == "unknown" {
		return nil
	}
	return &observed
}
