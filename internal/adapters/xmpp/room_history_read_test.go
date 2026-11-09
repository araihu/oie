package xmpp

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func receiveHistoryTestWire(t *testing.T, m *Manager, e *entry, raw string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		t.Fatal(err)
	}
	var sink bytes.Buffer
	start := token.(xml.StartElement)
	if err = m.handle(t.Context(), e, tokens{decoder, xml.NewEncoder(&sink)}, &start); err != nil {
		t.Fatal(err)
	}
}

func TestRoomHistoryDoesNotBecomeFreshUnreadTraffic(t *testing.T) {
	for _, tc := range []struct {
		name, from, stamp string
		historical        bool
	}{
		{"room history", "room@conference.example.test", "2026-11-08T13:48:21.889Z", true},
		{"other room", "other@conference.example.test", "2026-11-08T13:48:21.889Z", false},
		{"occupant delay", "room@conference.example.test/Bob", "2026-11-08T13:48:21.889Z", false},
		{"server delay", "example.test", "2026-11-08T13:48:21.889Z", false},
		{"missing authority", "", "2026-11-08T13:48:21.889Z", false},
		{"malformed time", "room@conference.example.test", "not-a-time", false},
		{"live message", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, e, slot, host := roomFixture(t)
			slot.snapshot.State = "joined"
			delay := ""
			if tc.stamp != "" {
				delay = `<delay xmlns="urn:xmpp:delay" from="` + tc.from + `" stamp="` + tc.stamp + `"/>`
			}
			raw := `<message xmlns="jabber:client" from="room@conference.example.test/Bob" to="alice@example.test/desktop" type="groupchat" id="history-read-case"><body>Retained room message</body>` + delay + `</message>`
			receiveHistoryTestWire(t, m, e, raw)
			if len(host.received) != 1 {
				t.Fatalf("expected one retained message, got %d", len(host.received))
			}
			in := host.received[0]
			if in.Historical != tc.historical || in.IsOwn || in.Kind != "room" || in.OriginalText != "Retained room message" {
				t.Fatalf("incorrect history classification: %+v", in)
			}
			if at, err := time.Parse(time.RFC3339Nano, tc.stamp); err == nil && !in.CreatedAt.Equal(at) {
				t.Fatal("history classification changed the protocol timestamp", in.CreatedAt, at)
			}
		})
	}
}

func TestDelayedDirectMessageRemainsUnreadEligible(t *testing.T) {
	m, e, _, host := roomFixture(t)
	receiveHistoryTestWire(t, m, e, `<message xmlns="jabber:client" from="bob@example.test/phone" to="alice@example.test/desktop" type="chat" id="offline-direct"><body>Sent while offline</body><delay xmlns="urn:xmpp:delay" from="example.test" stamp="2026-11-08T13:48:21.889Z"/></message>`)
	if len(host.received) != 1 || host.received[0].Historical || host.received[0].IsOwn || host.received[0].Address != "bob@example.test" {
		t.Fatal("offline direct delivery was incorrectly treated as room history", host.received)
	}
}
