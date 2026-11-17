package xmpp

import (
	"context"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

const displayedMarkerNamespace = "urn:xmpp:chat-markers:0"

type displayedMarkerMetadata struct {
	Markable  []wireID `xml:"urn:xmpp:chat-markers:0 markable"`
	Displayed []wireID `xml:"urn:xmpp:chat-markers:0 displayed"`
}

func (meta displayedMarkerMetadata) requestedID(id string) string {
	if len(meta.Markable) != 1 || len(meta.Displayed) != 0 || !usableID(id) {
		return ""
	}
	request := meta.Markable[0]
	if request.ID != "" || request.By != "" || request.Text != "" || len(request.Children) != 0 {
		return ""
	}
	return id
}

type displayedStanza struct {
	stanza.Message
	Displayed struct {
		ID string `xml:"id,attr"`
	} `xml:"urn:xmpp:chat-markers:0 displayed"`
	NoStore struct{} `xml:"urn:xmpp:hints no-store"`
}

func (m *Manager) DisplayedMarkerTransport(account domain.ID) string {
	e := m.typingEntry(account)
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.session == nil || e.raw == nil || e.state.State != "connected" || e.replaying {
		return ""
	}
	return e.displayedTransport
}

func (m *Manager) SendDisplayedMarker(ctx context.Context, c domain.Conversation, marker domain.DisplayedMarker) {
	host, ok := m.host.(application.DisplayedMarkerHost)
	if !ok || !usableID(marker.RemoteID) || marker.Transport == "" {
		return
	}
	to, err := jid.Parse(c.Address)
	if err != nil || to.Localpart() == "" || to.Resourcepart() != "" {
		return
	}
	e := m.typingEntry(c.AccountID)
	if e == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	e.sendMu.Lock()
	defer e.sendMu.Unlock()
	e.mu.RLock()
	session, raw := e.session, e.raw
	connected := session != nil && raw != nil && e.state.State == "connected" && !e.replaying && e.displayedTransport == marker.Transport
	kind, allowed := e.displayedDestination(c)
	e.mu.RUnlock()
	if !connected || !allowed || ctx.Err() != nil {
		return
	}

	if current, err := host.ClaimDisplayedMarker(ctx, marker); err != nil || !current {
		return
	}

	// Roster pushes and room changes can arrive while the database commits
	// the claim. Check the destination and connection again at the wire.
	e.mu.RLock()
	_, allowed = e.displayedDestination(c)
	current := e.session == session && e.raw == raw && e.displayedTransport == marker.Transport
	e.mu.RUnlock()
	if !current || !allowed || ctx.Err() != nil {
		return
	}
	payload := displayedStanza{Message: stanza.Message{To: to, Type: kind}}
	payload.Displayed.ID = marker.RemoteID
	err = session.Encode(ctx, payload)
	// The submitted stanza uses normal XEP-0198 resumption. Abandoning that
	// stream after a marker would also discard unrelated unacknowledged sends.
	if err != nil && !definitivelyUnsent(session.StreamManagement().Enabled, err) {
		_ = raw.Close()
	}
}

// The caller holds e.mu. A direct marker can reveal presence and is limited
// to contacts authorized to see it. A room marker uses a verified room ID.
func (e *entry) displayedDestination(c domain.Conversation) (stanza.MessageType, bool) {
	switch c.Kind {
	case "direct":
		return stanza.ChatMessage, e.readReceiptAudience[c.Address]
	case "room":
		slot := e.rooms[c.Address]
		if slot == nil || slot.conversation.ID != c.ID || slot.session != e.session || slot.snapshot.State != "joined" || slot.leaving {
			return stanza.GroupChatMessage, false
		}
		e.historyMu.Lock()
		stable := e.sidScopes[c.Address] && !e.roomConfigurations[c.Address].pending
		e.historyMu.Unlock()
		return stanza.GroupChatMessage, stable
	default:
		return "", false
	}
}
