package xmpp

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestPublishedFullNameUsesPublishedNamesAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		valid            bool
	}{
		{"formatted", `<FN>  Júlia da Silva  </FN><JABBERID>someone-else@example.test</JABBERID>`, "Júlia da Silva", true},
		{"structured", `<N><PREFIX>Dra.</PREFIX><GIVEN>Júlia</GIVEN><MIDDLE>Ana</MIDDLE><FAMILY>Silva</FAMILY><SUFFIX>Jr.</SUFFIX></N>`, "Dra. Júlia Ana Silva Jr.", true},
		{"empty formatted fallback", `<FN> </FN><N><GIVEN>Julia</GIVEN><FAMILY>Silva</FAMILY></N>`, "Julia Silva", true},
		{"no name", `<JABBERID>someone-else@example.test</JABBERID>`, "", true},
		{"controls", `<FN>Julia&#10;Silva</FN>`, "", false},
		{"structured controls", `<N><GIVEN>Julia&#9;Silva</GIVEN></N>`, "", false},
		{"overlong", `<FN>` + strings.Repeat("名", 301) + `</FN>`, "", false},
		{"ambiguous", `<FN>A</FN><FN>B</FN>`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := publishedFullName(`<vCard xmlns="vcard-temp">` + tc.body + `</vCard>`)
			if (err == nil) != tc.valid || got != tc.want {
				t.Fatalf("unexpected profile parse: name=%q err=%v", got, err)
			}
		})
	}
	for _, bad := range []string{`<vCard xmlns="wrong"><FN>Other namespace</FN></vCard>`, `<vCard xmlns="vcard-temp"/><vCard xmlns="vcard-temp"/>`, strings.Repeat("x", (2<<20)+1), `<vCard xmlns="vcard-temp">`} {
		if _, err := publishedFullName(bad); err == nil {
			t.Fatal("invalid or unbounded profile accepted")
		}
	}
}
func publishedProfileFixture(t *testing.T, respond func(stanza.IQ, int) string) (*Manager, *entry, <-chan int) {
	t.Helper()
	s, _, requests := contactSession(t, respond)
	m := New(t.TempDir(), nil)
	a := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Protocol: "xmpp", Address: "alice@example.test"}
	e := &entry{account: a, session: s, state: domain.Connection{State: "connected", UpdatedAt: time.Now().UTC()}}
	m.entries[a.ID] = e
	return m, e, requests
}
func profileResult(iq stanza.IQ, from, body string) string {
	return fmt.Sprintf(`<iq xmlns="jabber:client" type="result" id="%s" from="%s"><vCard xmlns="vcard-temp">%s</vCard></iq>`, iq.ID, from, body)
}
func TestPublishedProfileCacheAndAuthenticatedRead(t *testing.T) {
	m, e, requests := publishedProfileFixture(t, func(iq stanza.IQ, _ int) string {
		if iq.To.String() != "bob@example.test" {
			t.Errorf("unexpected profile target %s", iq.To)
		}
		return profileResult(iq, "bob@example.test", `<FN>Bob da Silva</FN><JABBERID>mallory@example.test</JABBERID>`)
	})
	for range 2 {
		got, err := m.PublishedProfile(t.Context(), e.account, "bob@example.test")
		if err != nil || got.State != "known" || got.FullName != "Bob da Silva" || got.PersonalAddress != "" {
			t.Fatal("profile address or data was not authoritative", got, err)
		}
	}
	if len(requests) != 1 {
		t.Fatalf("profile cache did not bound repeat queries: %d", len(requests))
	}
	e.mu.Lock()
	e.profiles["bob@example.test"].expires = time.Now().Add(-time.Second)
	e.mu.Unlock()
	if _, err := m.PublishedProfile(t.Context(), e.account, "bob@example.test"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatal("expired profile did not refresh")
	}
	before := len(requests)
	for _, target := range []string{"bob@example.test/device", "room@conference.example.test/Bob", "example.test", " bob@example.test"} {
		if _, err := m.PublishedProfile(t.Context(), e.account, target); err == nil {
			t.Fatal("noncanonical personal target accepted", target)
		}
	}
	a := e.account
	a.Address = "other@example.test"
	got, _ := m.PublishedProfile(t.Context(), a, "bob@example.test")
	if got.State != "not_connected" || len(requests) != before {
		t.Fatal("foreign account reached profile cache")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.PublishedProfile(ctx, e.account, "bob@example.test"); err == nil {
		t.Fatal("cancelled read returned cached profile")
	}
	e.mu.Lock()
	e.state.State = "not_connected"
	e.mu.Unlock()
	got, _ = m.PublishedProfile(t.Context(), e.account, "bob@example.test")
	if got.State != "not_connected" || got.FullName != "" {
		t.Fatal("disconnected session exposed stale cache")
	}
}
func TestPublishedProfileRejectsStaleSessionsAndCoalesces(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m, e, requests := publishedProfileFixture(t, func(iq stanza.IQ, n int) string {
		if n == 0 {
			close(entered)
			<-release
		}
		return profileResult(iq, "bob@example.test", `<FN>Old session name</FN>`)
	})
	var wg sync.WaitGroup
	results := make(chan domain.SenderProfile, 2)
	for range 2 {
		wg.Go(func() { got, _ := m.PublishedProfile(t.Context(), e.account, "bob@example.test"); results <- got })
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("profile did not start")
	}
	e.mu.Lock()
	e.session = &mx.Session{}
	e.state.State = "not_connected"
	e.mu.Unlock()
	close(release)
	wg.Wait()
	close(results)
	for got := range results {
		if got.FullName != "" || got.State != "not_connected" {
			t.Fatal("retired session profile escaped", got)
		}
	}
	if len(requests) != 1 {
		t.Fatal("concurrent queries were not coalesced")
	}
}
func TestPublishedProfileRejectsWrongIQSenderAndCachesFailure(t *testing.T) {
	m, e, requests := publishedProfileFixture(t, func(iq stanza.IQ, _ int) string {
		return profileResult(iq, "mallory@example.test", `<FN>Wrong identity</FN>`)
	})
	for range 2 {
		got, err := m.PublishedProfile(t.Context(), e.account, "bob@example.test")
		if err != nil || got.State != "unavailable" || got.FullName != "" {
			t.Fatal("foreign IQ disclosed profile", got, err)
		}
	}
	if len(requests) != 1 {
		t.Fatal("failed profile was not briefly cached")
	}
}
func TestLiveRoomSenderUsesCurrentPublicIdentityOnly(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	slot.snapshot.State = "joined"
	e.state.UpdatedAt = time.Now().Add(-time.Second)
	info := domain.ConversationInfo{State: "known", CheckedAt: time.Now(), Features: []string{"non_anonymous"}}
	e.details = map[domain.ID]*detailSlot{slot.conversation.ID: {conversation: slot.conversation, info: info}}
	var p incomingPresence
	raw := `<presence xmlns="jabber:client" from="` + slot.conversation.Address + `/Juliet"><x xmlns="http://jabber.org/protocol/muc#user"><item jid="juliet@example.test/device"/></x></presence>`
	if err := xml.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	captureRoomMentionPresence(e, p)
	from := jid.MustParse(slot.conversation.Address + "/Juliet")
	if got := liveRoomSenderAddress(e, from, "", ""); got != "juliet@example.test" {
		t.Fatalf("authorized observed target lost: %q", got)
	}
	if err := m.receiveRoomMessage(t.Context(), e, structMessage(from), replyMetadata{}, "fresh", "", "", fileSharingMetadata{}); err != nil {
		t.Fatal(err)
	}
	if len(host.received) != 1 || host.received[0].SenderAddress != "juliet@example.test" {
		t.Fatal("live incoming did not retain author address")
	}
	for _, tc := range []struct {
		features      []string
		stamp, delay  string
		departed, old bool
	}{
		{features: []string{"anonymous"}}, {features: []string{"anonymous", "non_anonymous"}}, {features: []string{"non_anonymous"}, stamp: "invalid-delay"}, {features: []string{"non_anonymous"}, delay: slot.conversation.Address}, {features: []string{"non_anonymous"}, departed: true}, {features: []string{"non_anonymous"}, old: true},
	} {
		e.details[slot.conversation.ID].info.Features = tc.features
		e.details[slot.conversation.ID].info.CheckedAt = time.Now()
		if tc.old {
			e.details[slot.conversation.ID].info.CheckedAt = e.state.UpdatedAt.Add(-time.Second)
		}
		value := slot.mentionOccupants[from.String()]
		value.present = !tc.departed
		slot.mentionOccupants[from.String()] = value
		if got := liveRoomSenderAddress(e, from, tc.stamp, tc.delay); got != "" {
			t.Fatal("unverified or delayed room identity disclosed", got)
		}
	}
}

func TestArchivedSenderAddressRequiresUnambiguousRoomMetadata(t *testing.T) {
	account := domain.Account{Address: "alice@localhost"}
	room := "room@conference.localhost"
	for _, tc := range []struct{ meta, want string }{
		{``, ""},
		{`<x xmlns="http://jabber.org/protocol/muc#user"><item jid="juliet@example.test/phone"/></x>`, "juliet@example.test"},
		{`<x xmlns="http://jabber.org/protocol/muc#user"><item jid="alice@localhost/device"/></x>`, "alice@localhost"},
		{`<x xmlns="wrong"><item jid="juliet@example.test"/></x>`, ""},
		{`<x xmlns="http://jabber.org/protocol/muc#user"><item jid="room@conference.localhost/Nick"/></x>`, ""},
		{`<x xmlns="http://jabber.org/protocol/muc#user"><item jid="example.test"/></x>`, ""},
		{`<x xmlns="http://jabber.org/protocol/muc#user"><item jid="a@example.test"/><item jid="b@example.test"/></x>`, ""},
		{`<x xmlns="http://jabber.org/protocol/muc#user"><item jid="a@example.test"/></x><x xmlns="http://jabber.org/protocol/muc#user"><item jid="a@example.test"/></x>`, ""},
	} {
		var result archiveResult
		raw := `<result xmlns="urn:xmpp:mam:2" id="one"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="2020-01-01T10:00:00Z"/><message xmlns="jabber:client" type="groupchat" from="` + room + `/NotTheAddress"><body>retained</body>` + tc.meta + `</message></forwarded></result>`
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		in, ok, err := archivedIncoming(account, room, &result, archiveTrust{})
		if err != nil || !ok || in.SenderAddress != tc.want {
			t.Fatalf("unexpected archived address %q want %q, err %v", in.SenderAddress, tc.want, err)
		}
	}
}
