package xmpp

import (
	"fmt"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestTypingPeerNegotiationAndCapacity(t *testing.T) {
	e := &entry{state: domain.Connection{State: "connected"}, session: new(mx.Session)}
	const address = "older@localhost"
	sender, _ := jid.Parse(address)
	e.typingPeers = map[string]typingPeer{address: {offered: true}}
	e.captureTyping(stanza.Message{Type: stanza.ChatMessage, From: sender}, "Plain reply", typingMetadata{})
	if got := e.typingPeers[address]; !got.declined || got.supported {
		t.Fatal(got)
	}
	e.typingPeers[address] = typingPeer{}
	for i := range 256 {
		peer, _ := jid.Parse(fmt.Sprintf("peer-%d@localhost/device", i))
		e.captureTyping(stanza.Message{Type: stanza.ChatMessage, From: peer}, "", typingMetadata{Composing: &chatState{}})
	}
	if _, ok := e.typingPeers[address]; ok || len(e.typingPeers) != 256 {
		t.Fatal("old peer was not evicted")
	}
	if peer := e.typingPeers["peer-255@localhost"]; !peer.supported || !peer.until.After(time.Now()) {
		t.Fatal("new peer did not become active", peer)
	}
}

func TestTypingExpiresAndWaitsForCumulativeAcknowledgment(t *testing.T) {
	now := time.Now()
	peer := typingPeer{sent: true, updatedAt: now}
	if peer.needsTyping(true, now) || !peer.needsTyping(true, now.Add(typingLifetime)) || !peer.needsTyping(false, now) {
		t.Fatal("typing can become stuck or bypass deduplication")
	}
	last := uint32(0)
	if typingAwaitingAck(nil, 0) || !typingAwaitingAck(&last, ^uint32(0)) ||
		typingAwaitingAck(&last, 0) || typingAwaitingAck(&last, 1) {
		t.Fatal("acknowledgment failed across counter wrap")
	}
}
