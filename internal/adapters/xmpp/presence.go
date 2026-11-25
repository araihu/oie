package xmpp

import (
	"context"
	"encoding/xml"
	"strconv"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stanza"
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
	if e.state.State != "connected" && !e.replaying {
		return
	}
	for _, slot := range e.details {
		known = known || (slot.conversation.Kind == "direct" && slot.conversation.Address == address)
	}
	known = known || e.contactSubscriptions[address]
	// Hold early roster presence separately until the roster authorizes it.
	if !known && e.initialPeerPresence == nil {
		return
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
	observed := peerResource{observation: domain.PresenceObservation{State: state, StatusText: cleanEvidence(p.Status, 300), ObservedAt: time.Now().UTC()}, priority: priority}
	if !known {
		storePeerPresence(e.initialPeerPresence, address, p.From.Resourcepart(), observed)
		return
	}
	if e.peerPresence == nil {
		e.peerPresence = make(map[string]map[string]peerResource)
	}
	storePeerPresence(e.peerPresence, address, p.From.Resourcepart(), observed)
}

func storePeerPresence(peers map[string]map[string]peerResource, address, resource string, observed peerResource) {
	resources := peers[address]
	if resources == nil {
		if len(peers) >= 256 {
			return
		}
		resources = make(map[string]peerResource)
		peers[address] = resources
	}
	current, exists := resources[resource]
	if !exists && len(resources) >= 16 || exists && observed.observation.ObservedAt.Before(current.observation.ObservedAt) {
		return
	}
	resources[resource] = observed
}

// Caller holds e.mu.
func (e *entry) updateContactSubscriptions(items []application.ContactInput) {
	subscriptions := make(map[string]bool, len(items))
	audience := make(map[string]bool, len(items))
	for _, item := range items {
		if item.Subscription == "from" || item.Subscription == "both" {
			delete(e.contactRequests, item.Address)
			audience[item.Address] = true
		}
		if item.Subscription != "to" && item.Subscription != "both" {
			continue
		}
		subscriptions[item.Address] = true
		for resource, observation := range e.initialPeerPresence[item.Address] {
			if e.peerPresence == nil {
				e.peerPresence = make(map[string]map[string]peerResource)
			}
			storePeerPresence(e.peerPresence, item.Address, resource, observation)
		}
	}
	for address := range e.contactSubscriptions {
		if !subscriptions[address] {
			delete(e.peerPresence, address)
		}
	}
	e.contactSubscriptions = subscriptions
	e.readReceiptAudience = audience
	e.initialPeerPresence = nil
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

func (m *Manager) configuredPresence(ctx context.Context, account domain.ID) (string, error) {
	if host, ok := m.host.(application.AccountPresenceHost); ok {
		preferences, err := host.AccountCommunicationPreferences(ctx, account)
		if err != nil {
			return "", err
		}
		switch preferences.Presence {
		case "away", "dnd":
			return preferences.Presence, nil
		}
	}
	return "", nil
}

func presencePayload(show string, protected bool) xml.TokenReader {
	caps := clientCaps(localClientInfo(protected)).TokenReader()
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

func (m *Manager) RefreshAccountPresence(account domain.Account) {
	m.mu.RLock()
	e := m.entries[account.ID]
	m.mu.RUnlock()
	if e == nil {
		return
	}
	e.mu.RLock()
	wake := e.presenceWake
	e.mu.RUnlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (m *Manager) presenceLoop(ctx context.Context, e *entry, s *mx.Session, wake <-chan struct{}) {
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := m.applyPresence(attempt, e, s)
		cancel()
		if err == nil {
			select {
			case <-ctx.Done():
			case <-wake:
			}
			continue
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
		case <-wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (m *Manager) applyPresence(ctx context.Context, e *entry, s *mx.Session) error {
	e.presenceMu.Lock()
	defer e.presenceMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	show, err := m.configuredPresence(ctx, e.account.ID)
	if err != nil {
		return err
	}
	if e.presenceShow == show {
		return nil
	}
	err = s.Send(ctx, stanza.Presence{Type: stanza.AvailablePresence}.Wrap(presencePayload(show, m.omemoDir != "")))
	if err == nil {
		e.presenceShow = show
	}
	return err
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
