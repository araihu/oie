package xmpp

import (
	"context"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

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

func (m *Manager) SendDisplayedMarker(ctx context.Context, c domain.Conversation, marker domain.DisplayedMarker) {
	host, ok := m.host.(application.AccountPresenceHost)
	if !ok || !usableID(marker.RemoteID) {
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
	prefs, err := host.AccountCommunicationPreferences(ctx, c.AccountID)
	if err != nil || !prefs.SendReadReceipts || prefs.RevisionID != marker.PreferencesRevision {
		return
	}
	e.mu.RLock()
	session, raw := e.session, e.raw
	connected := session != nil && raw != nil && e.state.State == "connected"
	kind, allowed := e.displayedDestination(c)
	e.mu.RUnlock()
	if !connected || !allowed || ctx.Err() != nil {
		return
	}

	payload := displayedStanza{Message: stanza.Message{To: to, Type: kind}}
	payload.Displayed.ID = marker.RemoteID
	err = session.Encode(ctx, payload)
	status := session.StreamManagement()
	e.mu.Lock()
	if e.session == session {
		e.trackEphemeralStanza(status, err)
	}
	e.mu.Unlock()
	if err != nil && !definitivelyUnsent(status.Enabled, err) {
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

// The caller holds e.mu. An unacknowledged ephemeral stanza must not be
// resent by stream resumption after its originating connection drops.
func (e *entry) trackEphemeralStanza(status mx.StreamManagementStatus, err error) {
	if status.Enabled && status.Unacknowledged != 0 && !definitivelyUnsent(true, err) {
		last := status.Acknowledged + uint32(status.Unacknowledged)
		e.pendingEphemeral = &last
	}
}
