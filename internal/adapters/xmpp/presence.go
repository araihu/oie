package xmpp

import (
	"github.com/araihu/oie/internal/domain"
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
	Show     string   `xml:"jabber:client show"`
	Status   string   `xml:"jabber:client status"`
	Priority string   `xml:"jabber:client priority"`
	MUC      *mucUser `xml:"http://jabber.org/protocol/muc#user x"`
	Error    *struct {
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
	if !known {
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
