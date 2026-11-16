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
	supported bool
	until     time.Time
	sent      bool
	updatedAt time.Time
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
	if !known && len(e.typingPeers) >= 256 {
		return
	}
	peer.supported = peer.supported || supported
	peer.until = time.Time{}
	if state.Composing != nil && body == "" {
		peer.until = time.Now().Add(typingLifetime)
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

func (m *Manager) SetTyping(ctx context.Context, conversation domain.Conversation, active bool) {
	e := m.typingEntry(conversation.AccountID)
	if e == nil {
		return
	}
	target, err := jid.Parse(conversation.Address)
	if err != nil {
		return
	}
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	e.mu.Lock()
	session, raw := e.session, e.raw
	peer := e.typingPeers[conversation.Address]
	now := time.Now()
	if session == nil || raw == nil || e.state.State != "connected" || !peer.supported || peer.sent == active || active && now.Sub(peer.updatedAt) < 500*time.Millisecond {
		e.mu.Unlock()
		return
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
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = now.Add(3 * time.Second)
	}
	_ = raw.SetWriteDeadline(deadline)
	defer func() { _ = raw.SetWriteDeadline(time.Time{}) }()
	if err := session.Encode(ctx, payload); err != nil {
		return
	}
	e.mu.Lock()
	if e.session == session {
		peer := e.typingPeers[conversation.Address]
		peer.sent, peer.updatedAt = active, now
		e.typingPeers[conversation.Address] = peer
	}
	e.mu.Unlock()
}

func (m *Manager) outgoingChatState(ctx context.Context, a domain.Account, out application.Outgoing) *chatState {
	if out.Kind != "direct" || out.Reaction != nil {
		return nil
	}
	if host, ok := m.host.(application.AccountPresenceHost); ok {
		if prefs, err := host.AccountCommunicationPreferences(ctx, a.ID); err == nil && prefs.SendTyping {
			return &chatState{XMLName: xml.Name{Space: typingNamespace, Local: "active"}}
		}
	}
	return nil
}
