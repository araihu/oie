package xmpp

import (
	"context"
	"encoding/xml"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const typingLifetime = 12 * time.Second
const typingNamespace = "http://jabber.org/protocol/chatstates"

type chatState struct {
	XMLName xml.Name
}

type typingMetadata struct {
	Active    *chatState `xml:"http://jabber.org/protocol/chatstates active"`
	Composing *chatState `xml:"http://jabber.org/protocol/chatstates composing"`
	Paused    *chatState `xml:"http://jabber.org/protocol/chatstates paused"`
	Inactive  *chatState `xml:"http://jabber.org/protocol/chatstates inactive"`
	Gone      *chatState `xml:"http://jabber.org/protocol/chatstates gone"`
}

type typingPeer struct {
	offered   bool
	supported bool
	declined  bool
	until     time.Time
	sent      bool
	updatedAt time.Time
	lastSeen  time.Time
}

// Caller holds e.mu. Expire the least recently used entry when a new peer arrives.
func (e *entry) typingPeer(address string) (typingPeer, bool) {
	if peer, ok := e.typingPeers[address]; ok {
		return peer, true
	}
	if e.typingPeers == nil {
		e.typingPeers = make(map[string]typingPeer)
	}
	if len(e.typingPeers) >= 256 {
		var oldestAddress string
		var oldestTime time.Time
		for candidate, peer := range e.typingPeers {
			if oldestAddress == "" || peer.lastSeen.Before(oldestTime) {
				oldestAddress, oldestTime = candidate, peer.lastSeen
			}
		}
		delete(e.typingPeers, oldestAddress)
	}
	return typingPeer{}, false
}

func (e *entry) captureTyping(message stanza.Message, body string, state typingMetadata) {
	address := message.From.Bare().String()
	if message.Type != stanza.ChatMessage || message.From.Localpart() == "" || address == e.account.Address {
		return
	}
	supported := state.Active != nil || state.Composing != nil || state.Paused != nil || state.Inactive != nil || state.Gone != nil
	if !supported && body == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.state.State != "connected" {
		return
	}
	peer, known := e.typingPeers[address]
	if !known && !supported {
		return
	}
	if !known {
		peer, _ = e.typingPeer(address)
	}
	now := time.Now()
	peer.lastSeen = now
	if supported {
		peer.supported, peer.declined = true, false
	} else if peer.offered && !peer.supported {
		peer.declined = true
	}
	peer.until = time.Time{}
	if state.Composing != nil && body == "" {
		peer.until = now.Add(typingLifetime)
	}
	e.typingPeers[address] = peer
}

func (m *Manager) typingEntry(account domain.ID) *entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.entries[account]
}

func (m *Manager) Typing(conversation domain.Conversation) bool {
	e := m.typingEntry(conversation.AccountID)
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state.State == "connected" && e.typingPeers[conversation.Address].until.After(time.Now())
}

func (m *Manager) SetTyping(ctx context.Context, conversation domain.Conversation, active bool) error {
	e := m.typingEntry(conversation.AccountID)
	if e == nil {
		return nil
	}
	target, err := jid.Parse(conversation.Address)
	if err != nil {
		return application.ErrInvalid
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	e.mu.Lock()
	session, raw := e.session, e.raw
	peer := e.typingPeers[conversation.Address]
	now := time.Now()
	if session == nil || raw == nil || e.state.State != "connected" || !peer.supported || !peer.needsTyping(active, now) {
		// A typing update expires without a connected session to send it.
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()
	state := "paused"
	if active {
		state = "composing"
	}
	payload := struct {
		stanza.Message
		State chatState
		Hint  struct{} `xml:"urn:xmpp:hints no-store"`
	}{
		Message: stanza.Message{To: target, Type: stanza.ChatMessage},
		State:   chatState{XMLName: xml.Name{Space: typingNamespace, Local: state}},
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err = session.Encode(ctx, payload)
	status := session.StreamManagement()
	e.mu.Lock()
	if e.session == session {
		// Stream management replays exact stanza counts, including chat states.
		// If their acknowledgement is uncertain, reconnect with a fresh stream.
		if status.Enabled && status.Unacknowledged != 0 && !definitivelyUnsent(true, err) {
			last := status.Acknowledged + uint32(status.Unacknowledged)
			e.pendingTyping = &last
		}
		if peer, ok := e.typingPeers[conversation.Address]; ok && err == nil {
			peer.sent, peer.updatedAt, peer.lastSeen = active, now, now
			e.typingPeers[conversation.Address] = peer
		}
	}
	e.mu.Unlock()
	if err != nil {
		if !definitivelyUnsent(status.Enabled, err) {
			_ = raw.Close()
		}
		return application.ErrProtocolUnavailable
	}
	return nil
}

func (peer typingPeer) needsTyping(active bool, now time.Time) bool {
	if !active {
		return peer.sent
	}
	elapsed := now.Sub(peer.updatedAt)
	return elapsed >= 500*time.Millisecond && (!peer.sent || elapsed >= typingLifetime)
}

func stanzaAwaitingAck(last *uint32, ack uint32) bool {
	// The replay queue is bounded well below half the counter's range.
	return last != nil && int32(ack-*last) < 0
}

func (m *Manager) outgoingChatState(ctx context.Context, e *entry, a domain.Account, out application.Outgoing) *chatState {
	if out.Kind != "direct" || out.Reaction != nil {
		return nil
	}
	e.mu.RLock()
	declined := e.typingPeers[out.Address].declined
	e.mu.RUnlock()
	if declined {
		return nil
	}
	if host, ok := m.host.(application.AccountPresenceHost); ok {
		if prefs, err := host.AccountCommunicationPreferences(ctx, a.ID); err == nil && prefs.SendTyping {
			return &chatState{XMLName: xml.Name{Space: typingNamespace, Local: "active"}}
		}
	}
	return nil
}
