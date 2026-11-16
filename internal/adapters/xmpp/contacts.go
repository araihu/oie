package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const contactRosterNS = "jabber:iq:roster"

type contactRosterItem struct {
	Address      string   `xml:"jid,attr"`
	Name         string   `xml:"name,attr,omitempty"`
	Subscription string   `xml:"subscription,attr,omitempty"`
	Groups       []string `xml:"jabber:iq:roster group"`
}
type contactRosterPayload struct {
	Items []contactRosterItem `xml:"jabber:iq:roster item"`
}

func decodeContactRoster(payload contactRosterPayload) ([]application.ContactInput, error) {
	if len(payload.Items) > 10000 {
		return nil, application.ErrInvalid
	}
	items := make([]application.ContactInput, 0, len(payload.Items))
	seen := map[string]bool{}
	for _, item := range payload.Items {
		address, err := jid.Parse(item.Address)
		if err != nil || address.Domainpart() == "" || address.Resourcepart() != "" || seen[address.String()] {
			return nil, application.ErrInvalid
		}
		seen[address.String()] = true
		subscription := item.Subscription
		switch subscription {
		case "none", "to", "from", "both":
		default:
			subscription = "none"
		}
		items = append(items, application.ContactInput{Address: address.Bare().String(), DisplayName: item.Name, Subscription: subscription, Groups: item.Groups})
	}
	return items, nil
}

func (m *Manager) readContacts(ctx context.Context, e *entry, s *mx.Session) ([]application.ContactInput, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := s.SendIQ(ctx, stanza.IQ{Type: stanza.GetIQ}.Wrap(xml.NewDecoder(strings.NewReader("<query xmlns='"+contactRosterNS+"'/>"))))
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Close() }()
	var envelope struct {
		stanza.IQ
		Rosters []contactRosterPayload `xml:"jabber:iq:roster query"`
	}
	if err = xml.NewTokenDecoder(&boundedTokens{reader: response, remaining: 2 << 20}).Decode(&envelope); err != nil {
		return nil, err
	}
	account, _ := jid.Parse(e.account.Address)
	if envelope.Type != stanza.ResultIQ || len(envelope.Rosters) != 1 || envelope.From.String() != "" && !envelope.From.Equal(account.Bare()) {
		return nil, errors.New("roster unavailable")
	}
	return decodeContactRoster(envelope.Rosters[0])
}

func (m *Manager) contactsLoop(ctx context.Context, e *entry, s *mx.Session, wake <-chan struct{}) {
	host, ok := m.host.(application.ContactHost)
	if !ok {
		return
	}
	e.mu.Lock()
	if e.session != s || e.contactsWake != wake {
		e.mu.Unlock()
		return
	}
	if e.contactsGate == nil {
		e.contactsGate = make(chan struct{}, 1)
	}
	gate := e.contactsGate
	e.mu.Unlock()
	for ctx.Err() == nil {
		if !claimContactGate(ctx, gate) {
			return
		}
		_, current, readErr := m.refreshContacts(ctx, host, e, s)
		<-gate
		if !current {
			return
		}
		// Coalesce pushes and avoid an unbounded refresh loop. Pushes are
		// acknowledged immediately; a fresh complete query avoids initial-query
		// versus push ordering races without holding a lock over network I/O.
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		var retry <-chan time.Time
		var retryTimer *time.Timer
		if readErr != nil {
			retryTimer = time.NewTimer(30 * time.Second)
			retry = retryTimer.C
		}
		select {
		case <-ctx.Done():
			if retryTimer != nil {
				retryTimer.Stop()
			}
			return
		case <-wake:
		case <-retry:
		}
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}
}

func claimContactGate(ctx context.Context, gate chan struct{}) bool {
	select {
	case gate <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *Manager) refreshContacts(ctx context.Context, host application.ContactHost, e *entry, s *mx.Session) ([]application.ContactInput, bool, error) {
	items, err := m.readContacts(ctx, e, s)
	// Hold session ownership through persistence so a replaced connection
	// cannot overwrite a newer provider snapshot.
	m.mu.RLock()
	defer m.mu.RUnlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	current := m.entries[e.account.ID] == e && e.session == s && e.state.State == "connected" && ctx.Err() == nil
	if current {
		if err == nil {
			err = host.ContactsReceived(ctx, e.account.ID, items)
			if err == nil {
				e.updateContactSubscriptions(items)
			}
		}
		if err != nil && ctx.Err() == nil {
			_ = host.ContactsUnavailable(ctx, e.account.ID)
		}
	}
	return items, current, err
}

func (m *Manager) ContactRequests(_ context.Context, a domain.Account) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e := m.entries[a.ID]
	if e == nil {
		return nil, application.ErrProtocolUnavailable
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.session == nil || e.state.State != "connected" {
		return nil, application.ErrProtocolUnavailable
	}
	items := make([]string, 0, len(e.contactRequests))
	for address := range e.contactRequests {
		items = append(items, address)
	}
	slices.Sort(items)
	return items, nil
}

func captureContactRequest(e *entry, p stanza.Presence) {
	if p.From.Localpart() == "" || p.From.Resourcepart() != "" || p.From.String() == e.account.Address {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.State != "connected" && !e.replaying {
		return
	}
	switch p.Type {
	case stanza.SubscribePresence:
		if e.contactRequests != nil && len(e.contactRequests) < 256 {
			e.contactRequests[p.From.String()] = struct{}{}
		}
	case stanza.UnsubscribePresence:
		delete(e.contactRequests, p.From.String())
	}
}

func (m *Manager) ChangeContact(ctx context.Context, a domain.Account, address, action string) error {
	target, err := jid.Parse(address)
	if err != nil || target.Resourcepart() != "" || target.Localpart() == "" || target.String() == a.Address {
		return application.ErrInvalid
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	if e == nil {
		return application.ErrProtocolUnavailable
	}
	e.mu.RLock()
	s, gate := e.session, e.contactsGate
	e.mu.RUnlock()
	if s == nil || gate == nil || !claimContactGate(ctx, gate) {
		return application.ErrProtocolUnavailable
	}
	defer func() { <-gate }()
	e.mu.RLock()
	current := e.session == s && e.contactsGate == gate && e.state.State == "connected"
	_, pending := e.contactRequests[target.String()]
	e.mu.RUnlock()
	if !current {
		return application.ErrProtocolUnavailable
	}
	switch action {
	case "accept", "decline":
		if !pending {
			return application.ErrStateConflict
		}
		host, ok := m.host.(application.ContactHost)
		if !ok {
			return application.ErrProtocolUnavailable
		}
		if _, current, err := m.refreshContacts(ctx, host, e, s); err != nil || !current {
			return application.ErrProtocolUnavailable
		}
		e.mu.RLock()
		current = e.session == s && e.contactsGate == gate && e.state.State == "connected"
		_, pending = e.contactRequests[target.String()]
		e.mu.RUnlock()
		if !current {
			return application.ErrProtocolUnavailable
		}
		if !pending {
			return application.ErrStateConflict
		}
		answer := stanza.SubscribedPresence
		if action == "decline" {
			answer = stanza.UnsubscribedPresence
		}
		if err := s.Send(ctx, stanza.Presence{To: target, Type: answer}.Wrap(nil)); err != nil {
			return application.ErrProtocolUnavailable
		}
		e.mu.Lock()
		if e.session == s {
			delete(e.contactRequests, target.String())
		}
		e.mu.Unlock()
		e.wakeContacts()
		return nil
	case "add", "remove":
		host, ok := m.host.(application.ContactHost)
		if !ok {
			return application.ErrProtocolUnavailable
		}
		items, current, err := m.refreshContacts(ctx, host, e, s)
		if err != nil || !current {
			return application.ErrProtocolUnavailable
		}
		present := slices.ContainsFunc(items, func(item application.ContactInput) bool {
			return item.Address == target.String()
		})
		if present == (action == "add") {
			return nil
		}
		if err := writeContactRoster(ctx, s, a.Address, target.String(), action); err != nil {
			return err
		}
		e.wakeContacts()
		_, _, _ = m.refreshContacts(ctx, host, e, s)
		return nil
	default:
		return application.ErrInvalid
	}
}

func writeContactRoster(ctx context.Context, s *mx.Session, accountAddress, address, action string) error {
	roster := struct {
		XMLName xml.Name            `xml:"jabber:iq:roster query"`
		Items   []contactRosterItem `xml:"item"`
	}{Items: []contactRosterItem{{Address: address}}}
	if action == "remove" {
		roster.Items[0].Subscription = "remove"
	}
	payload, err := xml.Marshal(roster)
	if err != nil {
		return err
	}
	response, err := s.SendIQ(ctx, stanza.IQ{Type: stanza.SetIQ}.Wrap(xml.NewDecoder(strings.NewReader(string(payload)))))
	if err != nil {
		return application.ErrProtocolUnavailable
	}
	defer func() { _ = response.Close() }()
	start, err := response.Token()
	tag, ok := start.(xml.StartElement)
	if err != nil || !ok {
		return application.ErrProtocolUnavailable
	}
	iq, err := stanza.NewIQ(tag)
	account, _ := jid.Parse(accountAddress)
	if err != nil || iq.From.String() != "" && !iq.From.Equal(account.Bare()) && !iq.From.Equal(account.Domain()) {
		return application.ErrProtocolUnavailable
	}
	if iq.Type == stanza.ErrorIQ {
		return application.ErrStateConflict
	}
	if iq.Type != stanza.ResultIQ {
		return application.ErrProtocolUnavailable
	}
	return nil
}

func (e *entry) wakeContacts() {
	e.mu.RLock()
	wake := e.contactsWake
	e.mu.RUnlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) rosterPush(e *entry, iq stanza.IQ, payload contactRosterPayload, t xmlstream.TokenReadEncoder) error {
	account, _ := jid.Parse(e.account.Address)
	// RFC 6121 2.1.6: unauthorised origins are ignored without replying, to
	// avoid a presence leak. A roster push contains exactly one item.
	if iq.Type != stanza.SetIQ || iq.From.String() != "" && !iq.From.Equal(account.Bare()) || len(payload.Items) != 1 {
		return nil
	}
	items, err := decodeContactRoster(payload)
	if err != nil {
		return nil
	}
	item := items[0]
	e.mu.Lock()
	if item.Subscription == "from" || item.Subscription == "both" {
		delete(e.contactRequests, item.Address)
	}
	if item.Subscription == "to" || item.Subscription == "both" {
		if e.contactSubscriptions == nil {
			e.contactSubscriptions = make(map[string]bool)
		}
		e.contactSubscriptions[item.Address] = true
	} else if e.contactSubscriptions[item.Address] {
		delete(e.contactSubscriptions, item.Address)
		delete(e.peerPresence, item.Address)
	}
	e.mu.Unlock()
	e.wakeContacts()
	_, err = xmlstream.Copy(t, iq.Result(nil))
	return err
}
