package xmpp

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	"mellium.im/xmpp/stream"
)

func TestContactRosterDecodingKeepsCompleteResultAndBounds(t *testing.T) {
	var payload contactRosterPayload
	input := `<query xmlns="jabber:iq:roster"><item jid="Bob@Example.test" name="Bob &amp; café" subscription="both"><group>Friends</group><group>Work</group></item><item jid="service.example.test"/><item jid="removed@example.test" subscription="remove"/><item jid="unknown@example.test" subscription="future"/></query>`
	if err := xml.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatal(err)
	}
	items, err := decodeContactRoster(payload)
	if err != nil || len(items) != 4 || items[0].Address != "bob@example.test" || items[0].DisplayName != "Bob & café" || len(items[0].Groups) != 2 || items[1].Subscription != "none" || items[2].Subscription != "none" || items[3].Subscription != "none" {
		t.Fatal("complete roster decoded incorrectly", items, err)
	}
	for _, items := range [][]contactRosterItem{{{Address: ""}}, {{Address: "bad@"}}, {{Address: "bob@example.test/resource"}}, {{Address: "Bob@example.test"}, {Address: "bob@example.test"}}, make([]contactRosterItem, 10001)} {
		if _, err := decodeContactRoster(contactRosterPayload{Items: items}); err == nil {
			t.Fatal("invalid or unbounded roster accepted", len(items))
		}
	}
}

func contactPushWire(t *testing.T, m *Manager, e *entry, wire string) (string, error) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(wire))
	token, err := decoder.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := token.(xml.StartElement)
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	err = m.handle(t.Context(), e, tokens{decoder, encoder}, &start)
	if flush := encoder.Flush(); err == nil {
		err = flush
	}
	return output.String(), err
}
func TestContactRosterPushOriginsSingleItemAndCoalescing(t *testing.T) {
	for _, tc := range []struct {
		name, from, kind, body string
		accept                 bool
	}{
		{"own server omitted", "", "set", `<item jid="bob@example.test"/>`, true},
		{"own bare", "alice@example.test", "set", `<item jid="bob@example.test" subscription="remove"/>`, true},
		{"foreign user", "mallory@example.test", "set", `<item jid="bob@example.test"/>`, false},
		{"own resource", "alice@example.test/other", "set", `<item jid="bob@example.test"/>`, false},
		{"server domain is not own bare", "example.test", "set", `<item jid="bob@example.test"/>`, false},
		{"foreign domain", "alice@attacker.test", "set", `<item jid="bob@example.test"/>`, false},
		{"not push", "", "get", `<item jid="bob@example.test"/>`, false},
		{"multiple items", "", "set", `<item jid="bob@example.test"/><item jid="carol@example.test"/>`, false},
		{"empty push", "", "set", ``, false},
		{"resource contact", "", "set", `<item jid="bob@example.test/phone"/>`, false},
		{"spoofed item namespace", "", "set", `<item xmlns="urn:unrelated" jid="bob@example.test"/>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &entry{account: domain.Account{Address: "alice@example.test"}, contactsWake: make(chan struct{}, 1)}
			m := New(t.TempDir(), nil)
			from := ""
			if tc.from != "" {
				from = ` from="` + tc.from + `"`
			}
			wire := `<iq xmlns="jabber:client" type="` + tc.kind + `" id="roster-push"` + from + `><query xmlns="jabber:iq:roster">` + tc.body + `</query></iq>`
			reply, err := contactPushWire(t, m, e, wire)
			if err != nil {
				t.Fatal(err)
			}
			if tc.accept {
				var iq stanza.IQ
				if xml.Unmarshal([]byte(reply), &iq) != nil || iq.Type != stanza.ResultIQ || iq.ID != "roster-push" || len(e.contactsWake) != 1 {
					t.Fatal("valid push was not acknowledged and queued", reply)
				}
				if _, err = contactPushWire(t, m, e, wire); err != nil || len(e.contactsWake) != 1 {
					t.Fatal("push wake queue is unbounded", err)
				}
			} else if reply != "" || len(e.contactsWake) != 0 {
				t.Fatal("untrusted or malformed push acknowledged or refreshed", reply)
			}
		})
	}
}

// A ready in-memory Mellium session exercises actual SendIQ correlation and XML
// reads while the peer supplies deterministic replies. No external server or
// account credentials are involved.
func contactSession(t *testing.T, respond func(stanza.IQ, int) string) (*mx.Session, net.Conn, <-chan int) {
	t.Helper()
	left, right := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	s, err := mx.NewSession(ctx, jid.MustParse("example.test"), jid.MustParse("alice@example.test/test"), left, mx.Ready, func(context.Context, *stream.Info, *stream.Info, *mx.Session, any) (mx.SessionState, io.ReadWriter, any, error) {
		return mx.Ready, nil, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan int, 32)
	var wg sync.WaitGroup
	wg.Go(func() {
		decoder := xml.NewDecoder(right)
		for n := 0; ; n++ {
			var iq stanza.IQ
			if err := decoder.Decode(&iq); err != nil {
				return
			}
			if iq.Type != stanza.GetIQ {
				t.Error("roster reader attempted non-get", iq.Type)
				return
			}
			requests <- n
			wire := respond(iq, n)
			if _, err := io.WriteString(right, wire); err != nil {
				return
			}
		}
	})
	wg.Go(func() {
		_ = s.Serve(mx.HandlerFunc(func(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			var ignored any
			return xml.NewTokenDecoder(xmlstream.MultiReader(xmlstream.Token(*start), t)).Decode(&ignored)
		}))
	})
	t.Cleanup(func() { cancel(); _ = left.Close(); _ = right.Close(); wg.Wait() })
	return s, left, requests
}
func contactResult(iq stanza.IQ, from, body string) string {
	if from != "" {
		from = ` from="` + from + `"`
	}
	return `<iq xmlns="jabber:client" type="result" id="` + iq.ID + `"` + from + `><query xmlns="jabber:iq:roster">` + body + `</query></iq>`
}
func TestContactReadUsesBoundedAuthenticatedCompleteRoster(t *testing.T) {
	for _, tc := range []struct {
		name, from, body string
		valid            bool
	}{
		{"empty complete", "", "", true}, {"own bare", "alice@example.test", `<item jid="bob@example.test" name="Bob"/>`, true},
		{"wrong origin", "mallory@example.test", `<item jid="bob@example.test"/>`, false},
		{"own full", "alice@example.test/other", `<item jid="bob@example.test"/>`, false},
		{"over byte budget", "", `<item jid="bob@example.test" name="` + strings.Repeat("x", (2<<20)+1) + `"/>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := contactSession(t, func(iq stanza.IQ, _ int) string { return contactResult(iq, tc.from, tc.body) })
			e := &entry{account: domain.Account{Address: "alice@example.test"}}
			m := New(t.TempDir(), nil)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second*3)
			defer cancel()
			items, err := m.readContacts(ctx, e, s)
			if tc.valid && err != nil || !tc.valid && err == nil {
				t.Fatalf("roster authentication or bounds: %d items %v", len(items), err)
			}
		})
	}
}

type contactHostProbe struct {
	hostStub
	received    chan []application.ContactInput
	unavailable chan struct{}
}

func (h *contactHostProbe) ContactsReceived(_ context.Context, _ domain.ID, items []application.ContactInput) error {
	h.received <- items
	return nil
}
func (h *contactHostProbe) ContactsUnavailable(context.Context, domain.ID) error {
	h.unavailable <- struct{}{}
	return nil
}
func contactLoopFixture(t *testing.T, respond func(stanza.IQ, int) string) (*Manager, *entry, *contactHostProbe, context.CancelFunc, <-chan struct{}, <-chan int) {
	t.Helper()
	s, _, requests := contactSession(t, respond)
	host := &contactHostProbe{received: make(chan []application.ContactInput, 8), unavailable: make(chan struct{}, 8)}
	m := New(t.TempDir(), nil)
	m.host = host
	e := &entry{account: domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@example.test"}, session: s, state: domain.Connection{State: "connected"}, contactsWake: make(chan struct{}, 1)}
	m.entries[e.account.ID] = e
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); m.contactsLoop(ctx, e, s, e.contactsWake) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second * 3):
			t.Error("roster worker did not stop")
		}
	})
	return m, e, host, cancel, done, requests
}
func TestContactLoopDiscardsRetiredEntryOrSessionWithoutLockingNetwork(t *testing.T) {
	for _, replacement := range []string{"entry", "session", "disconnected"} {
		t.Run(replacement, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			m, e, host, _, done, requests := contactLoopFixture(t, func(iq stanza.IQ, _ int) string {
				<-release
				return contactResult(iq, "", `<item jid="bob@example.test"/>`)
			})
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			select {
			case <-requests:
			case <-time.After(time.Second):
				t.Fatal("no initial roster read")
			}
			if !m.mu.TryLock() {
				t.Fatal("manager lock held during network read")
			}
			if !e.mu.TryLock() {
				m.mu.Unlock()
				t.Fatal("session lock held during network read")
			}
			switch replacement {
			case "entry":
				m.entries[e.account.ID] = &entry{account: e.account}
			case "session":
				e.session = &mx.Session{}
			case "disconnected":
				e.state.State = "not_connected"
			}
			e.mu.Unlock()
			m.mu.Unlock()
			releaseOnce.Do(func() { close(release) })
			select {
			case <-done:
			case <-time.After(time.Second * 3):
				t.Fatal("retired roster worker continued")
			}
			select {
			case <-host.received:
				t.Fatal("retired session overwrote directory")
			case <-host.unavailable:
				t.Fatal("retired session invalidated directory")
			default:
			}
		})
	}
}
func TestContactLoopCoalescesPushesAndStopsAfterFailedRead(t *testing.T) {
	_, e, host, cancel, done, requests := contactLoopFixture(t, func(iq stanza.IQ, n int) string {
		if n == 0 {
			return contactResult(iq, "", `<item jid="bob@example.test"/>`)
		}
		return `<iq xmlns="jabber:client" type="error" id="` + iq.ID + `"><error type="cancel"><service-unavailable xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`
	})
	select {
	case items := <-host.received:
		if len(items) != 1 {
			t.Fatal(items)
		}
	case <-time.After(time.Second):
		t.Fatal("initial snapshot missing")
	}
	<-requests
	start := time.Now()
	for range 50 {
		e.wakeContacts()
	}
	select {
	case <-requests:
		if time.Since(start) < 900*time.Millisecond {
			t.Fatal("push refresh ignored coalescing delay")
		}
	case <-time.After(time.Second * 3):
		t.Fatal("push refresh not read")
	}
	select {
	case <-host.unavailable:
	case <-time.After(time.Second):
		t.Fatal("failed read did not mark directory unavailable")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed roster retry was not cancellable")
	}
	select {
	case n := <-requests:
		t.Fatal(fmt.Sprint("unbounded or post-cancel retry ", n))
	default:
	}
}

func TestContactReadRejectsMissingOrForeignRosterPayload(t *testing.T) {
	for _, body := range []string{``, `<query xmlns="urn:foreign"><item jid="bob@example.test"/></query>`, `<query xmlns="jabber:iq:roster"><item jid="bob@example.test/resource"/></query>`} {
		t.Run(body, func(t *testing.T) {
			session, _, _ := contactSession(t, func(iq stanza.IQ, _ int) string {
				return `<iq xmlns="jabber:client" type="result" id="` + iq.ID + `">` + body + `</iq>`
			})
			m := New(t.TempDir(), nil)
			e := &entry{account: domain.Account{Address: "alice@example.test"}}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if items, err := m.readContacts(ctx, e, session); err == nil {
				t.Fatal("invalid payload became a complete snapshot", items)
			}
		})
	}
}
func TestContactRosterAcceptsExactlyTenThousandUniqueItems(t *testing.T) {
	payload := contactRosterPayload{Items: make([]contactRosterItem, 10000)}
	for i := range payload.Items {
		payload.Items[i] = contactRosterItem{Address: fmt.Sprintf("contact-%d@example.test", i)}
	}
	items, err := decodeContactRoster(payload)
	if err != nil || len(items) != 10000 {
		t.Fatal("maximum bounded roster rejected", len(items), err)
	}
}
