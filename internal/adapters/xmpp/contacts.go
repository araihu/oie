package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"strings"
	"time"

	"github.com/araihu/oie/internal/application"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const contactRosterNS = "jabber:iq:roster"

type contactRosterItem struct {
	Address      string   `xml:"jid,attr"`
	Name         string   `xml:"name,attr"`
	Subscription string   `xml:"subscription,attr"`
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
	for ctx.Err() == nil {
		items, readErr := m.readContacts(ctx, e, s)
		if ctx.Err() != nil {
			return
		}
		// Hold the owner/session observation through persistence. A replaced
		// connection cannot overwrite a newer connection's directory snapshot.
		m.mu.RLock()
		e.mu.RLock()
		current := m.entries[e.account.ID] == e && e.session == s && e.state.State == "connected" && ctx.Err() == nil
		if current {
			if readErr == nil {
				readErr = host.ContactsReceived(ctx, e.account.ID, items)
			}
			if readErr != nil && ctx.Err() == nil {
				_ = host.ContactsUnavailable(ctx, e.account.ID)
			}
		}
		e.mu.RUnlock()
		m.mu.RUnlock()
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
	if _, err := decodeContactRoster(payload); err != nil {
		return nil
	}
	e.wakeContacts()
	_, err := xmlstream.Copy(t, iq.Result(nil))
	return err
}
