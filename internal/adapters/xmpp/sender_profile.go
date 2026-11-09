package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const profileTTL = 5 * time.Minute
const failedProfileTTL = 30 * time.Second
const maxPublishedProfiles = 256

type profileSlot struct {
	session *mx.Session
	profile domain.SenderProfile
	expires time.Time
	pending chan struct{}
}

// Profiles are read only for an application-established canonical personal JID.
// No vCard field can redirect this lookup or establish another user's identity.
func (m *Manager) PublishedProfile(ctx context.Context, a domain.Account, address string) (domain.SenderProfile, error) {
	target, err := jid.Parse(address)
	if a.Protocol != "xmpp" || err != nil || target.Localpart() == "" || target.Domainpart() == "" || target.Resourcepart() != "" || target.String() != address {
		return domain.SenderProfile{}, application.ErrInvalid
	}
	m.mu.RLock()
	e := m.entries[a.ID]
	m.mu.RUnlock()
	off := domain.SenderProfile{State: "not_connected"}
	if e == nil || e.account.Address != a.Address {
		return off, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return domain.SenderProfile{}, err
		}
		e.mu.Lock()
		s := e.session
		if e.state.State != "connected" || s == nil {
			e.mu.Unlock()
			return off, nil
		}
		if e.profiles == nil {
			e.profiles = map[string]*profileSlot{}
		}
		now := time.Now()
		for key, slot := range e.profiles {
			if slot.pending == nil && (slot.session != s || !now.Before(slot.expires)) {
				delete(e.profiles, key)
			}
		}
		if cached := e.profiles[address]; cached != nil && cached.session == s {
			if cached.pending != nil {
				done := cached.pending
				e.mu.Unlock()
				select {
				case <-ctx.Done():
					return domain.SenderProfile{}, ctx.Err()
				case <-done:
					continue
				}
			}
			result := cached.profile
			e.mu.Unlock()
			m.mu.RLock()
			sameEntry := m.entries[a.ID] == e
			m.mu.RUnlock()
			if !sameEntry {
				return off, nil
			}
			return result, nil
		}
		if len(e.profiles) >= maxPublishedProfiles {
			e.mu.Unlock()
			return domain.SenderProfile{State: "unavailable"}, nil
		}
		slot := &profileSlot{session: s, pending: make(chan struct{})}
		e.profiles[address] = slot
		e.mu.Unlock()
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		payload, readErr := m.readHistoryIQ(queryCtx, e, s, address, stanza.GetIQ, `<vCard xmlns="vcard-temp"/>`)
		cancel()
		profile := domain.SenderProfile{State: "unavailable"}
		if readErr == nil {
			if name, parseErr := publishedFullName(payload); parseErr == nil {
				profile.State = "known"
				profile.FullName = name
			}
		}
		e.mu.Lock()
		current := e.session == s && e.state.State == "connected" && e.profiles[address] == slot
		done := slot.pending
		slot.pending = nil
		if current && ctx.Err() == nil {
			slot.profile = profile
			ttl := profileTTL
			if profile.State != "known" {
				ttl = failedProfileTTL
			}
			slot.expires = time.Now().Add(ttl)
		} else if e.profiles[address] == slot {
			delete(e.profiles, address)
		}
		close(done)
		e.mu.Unlock()
		m.mu.RLock()
		sameEntry := m.entries[a.ID] == e
		m.mu.RUnlock()
		if err := ctx.Err(); err != nil {
			return domain.SenderProfile{}, err
		}
		if !current || !sameEntry {
			return off, nil
		}
		return profile, nil
	}
}

func publishedFullName(payload string) (string, error) {
	if len(payload) > 2<<20 {
		return "", errors.New("profile unavailable")
	}
	var document struct {
		Cards []struct {
			Names      []string `xml:"FN"`
			Structured []struct {
				Prefix string `xml:"PREFIX"`
				Given  string `xml:"GIVEN"`
				Middle string `xml:"MIDDLE"`
				Family string `xml:"FAMILY"`
				Suffix string `xml:"SUFFIX"`
			} `xml:"N"`
		} `xml:"vcard-temp vCard"`
	}
	if err := xml.Unmarshal([]byte("<root>"+payload+"</root>"), &document); err != nil || len(document.Cards) != 1 {
		return "", errors.New("profile unavailable")
	}
	card := document.Cards[0]
	if len(card.Names) > 1 || len(card.Structured) > 1 {
		return "", errors.New("profile unavailable")
	}
	name := ""
	if len(card.Names) == 1 {
		name = strings.TrimSpace(card.Names[0])
	}
	if name == "" && len(card.Structured) == 1 {
		n := card.Structured[0]
		parts := []string{n.Prefix, n.Given, n.Middle, n.Family, n.Suffix}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
			if strings.IndexFunc(parts[i], unicode.IsControl) >= 0 {
				return "", errors.New("profile unavailable")
			}
		}
		name = strings.TrimSpace(strings.Join(parts, " "))
		name = strings.Join(strings.Fields(name), " ")
	}
	if len([]rune(name)) > 300 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("profile unavailable")
	}
	return name, nil
}

// A current presence observation can identify only a fresh message from that
// exact occupant in a confirmed public-identity room. Delayed history is never
// associated with whoever holds the nickname today.
func liveRoomSenderAddress(e *entry, from jid.JID, stamp, delayFrom string) string {
	if stamp != "" || delayFrom != "" {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	room := from.Bare().String()
	slot := e.rooms[room]
	if e.state.State != "connected" || e.session == nil || slot == nil || slot.session != e.session || slot.snapshot.State != "joined" {
		return ""
	}
	public := false
	for _, details := range e.details {
		if details.conversation.Address == room && details.conversation.Kind == "room" && (details.info.State == "known" || details.info.State == "partial") && !details.info.CheckedAt.IsZero() && details.info.CheckedAt.Compare(e.state.UpdatedAt) >= 0 {
			public = slices.Contains(details.info.Features, "non_anonymous") && !slices.Contains(details.info.Features, "anonymous")
			break
		}
	}
	if !public {
		return ""
	}
	observed, ok := slot.mentionOccupants[from.String()]
	if !ok || !observed.present {
		return ""
	}
	uri, ok := mentionTargetURI(observed.uri, room)
	if !ok {
		return ""
	}
	address, err := url.PathUnescape(strings.TrimPrefix(uri, "xmpp:"))
	if err != nil {
		return ""
	}
	return address
}

var _ application.ProfileProvider = (*Manager)(nil)
