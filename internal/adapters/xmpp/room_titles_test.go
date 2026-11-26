package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"mellium.im/xmpp/stanza"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
)

type roomTitleHostStub struct {
	hostStub
	application.AvatarHost
	needsFallback                 bool
	title, fallback, avatarSource string
}

func (h *roomTitleHostStub) NeedsRoomTitleFallback(context.Context, domain.ID, string) bool {
	return h.needsFallback
}
func (h *roomTitleHostStub) ObserveRoomTitle(_ context.Context, _ domain.ID, _ string, title string) error {
	h.title = title
	return nil
}
func (h *roomTitleHostStub) ObserveRoomTitleFallback(_ context.Context, _ domain.ID, _ string, title string) error {
	h.fallback = title
	return nil
}
func (h *roomTitleHostStub) Avatar(context.Context, domain.ID, string) (domain.Avatar, error) {
	return domain.Avatar{}, application.ErrNotFound
}
func (h *roomTitleHostStub) StoreAvatar(_ context.Context, _ domain.ID, _ string, source string, _ []byte) error {
	h.avatarSource = source
	return nil
}

func TestRoomTitleIndependentOfOccupantEvidenceCapacity(t *testing.T) {
	m, e, slot, _ := roomFixture(t)
	host := &roomTitleHostStub{}
	m.host = host
	e.occupantScopes = make(map[string]bool)
	for i := range maxRoomSessions {
		e.occupantScopes[fmt.Sprintf("occupied%d@example.test", i)] = true
	}
	room := slot.conversation.Address
	raw := `<query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text" name="Book club"/></query>`
	for _, address := range []string{"conference.example.test", room + "/nick", e.account.Address} {
		if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, address, raw, 0); err != nil {
			t.Fatal(err)
		}
		if host.title != "" {
			t.Fatalf("room title recorded for %s", address)
		}
	}
	if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, room, raw, 0); err != nil {
		t.Fatal(err)
	}
	if host.title != "Book club" || len(e.occupantScopes) != maxRoomSessions {
		t.Fatalf("name %q, %d occupant evidence slots", host.title, len(e.occupantScopes))
	}
}

func TestRoomVCardTitleDuringAvatarRefresh(t *testing.T) {
	const roomCard = `<vCard xmlns="vcard-temp"><FN> Book club </FN></vCard>`
	const pepRemoval = `<pubsub xmlns="http://jabber.org/protocol/pubsub"><items><item><metadata xmlns="urn:xmpp:avatar:metadata"/></item></items></pubsub>`
	for _, tc := range []struct {
		name, kind, pep, card, expectedTitle, expectedAvatar string
		missing                                              bool
		stale                                                string
	}{
		{"vCard avatar lookup", "room", "", roomCard, "Book club", "vcard:none", true, ""},
		{"PEP avatar with missing title", "room", pepRemoval, roomCard, "Book club", "pep:none", true, ""},
		{"PEP avatar with saved title", "room", pepRemoval, "", "", "pep:none", false, ""},
		{"PEP avatar without vCard", "room", pepRemoval, `<unsupported/>`, "", "pep:none", true, ""},
		{"PEP avatar with foreign FN", "room", pepRemoval, `<vCard xmlns="vcard-temp"><FN xmlns="other">Foreign</FN></vCard>`, "", "pep:none", true, ""},
		{"direct avatar", "direct", "", roomCard, "", "vcard:none", false, ""},
		{"stale configuration", "room", pepRemoval, roomCard, "", "pep:none", true, "configuration"},
		{"stale connection", "room", pepRemoval, roomCard, "", "pep:none", true, "session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e *entry
			m, entry, fixture := archiveFixture(t, func(iq testArchiveIQ, index int) string {
				payload := tc.pep
				if index > 0 {
					if tc.card == "" || index != 1 {
						t.Errorf("unexpected vCard IQ at index %d", index)
					}
					e.mu.Lock()
					switch tc.stale {
					case "configuration":
						advanceRoomPresenceLocked(e, iq.To.String())
					case "session":
						e.session = &mx.Session{}
					}
					e.mu.Unlock()
					payload = tc.card
				}
				return fmt.Sprintf(`<iq xmlns="jabber:client" from="%s" type="result" id="%s">%s</iq>`, iq.To, iq.ID, payload)
			})
			e = entry
			host := &roomTitleHostStub{needsFallback: tc.missing}
			m.host = host
			if err := m.fetchAvatar(t.Context(), host, e, e.session, domain.AvatarTarget{Address: fixture.room.Address, Kind: tc.kind}, ""); err != nil {
				t.Fatal(err)
			}
			if host.fallback != tc.expectedTitle || host.avatarSource != tc.expectedAvatar {
				t.Fatalf("fallback title %q, avatar source %q", host.fallback, host.avatarSource)
			}
		})
	}
}

func TestFailedPEPAvatarStillObservesRoomVCardTitle(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	m, e, fixture := archiveFixture(t, func(iq testArchiveIQ, index int) string {
		var payload string
		switch index {
		case 0:
			payload = `<pubsub xmlns="http://jabber.org/protocol/pubsub"><items><item><metadata xmlns="urn:xmpp:avatar:metadata"><info bytes="1" type="image/png" id="` + hash + `"/></metadata></item></items></pubsub>`
		case 1:
			payload = `<pubsub xmlns="http://jabber.org/protocol/pubsub"><items/></pubsub>`
		case 2:
			payload = `<vCard xmlns="vcard-temp"><FN>Book club</FN></vCard>`
		default:
			t.Errorf("unexpected IQ %d", index)
		}
		return fmt.Sprintf(`<iq xmlns="jabber:client" from="%s" type="result" id="%s">%s</iq>`, iq.To, iq.ID, payload)
	})
	host := &roomTitleHostStub{needsFallback: true}
	m.host = host
	err := m.fetchAvatar(t.Context(), host, e, e.session, domain.AvatarTarget{Address: fixture.room.Address, Kind: "room"}, "")
	if !errors.Is(err, errAvatar) || host.fallback != "Book club" || host.avatarSource != "" {
		t.Fatalf("avatar error %v, source %q, room title %q", err, host.avatarSource, host.fallback)
	}
}

func TestConfirmedJoinRechecksAddressOnlyRoomProfile(t *testing.T) {
	for _, tc := range []struct {
		title, hint              string
		fromProfile, wantRefresh bool
	}{
		{"room@conference.example.test", "", false, true},
		{"", "pending-hash", false, true},
		{"Saved name", "", false, false},
		{"Published name", "", true, true},
	} {
		m, e, slot, _ := roomFixture(t)
		room := slot.conversation.Address
		slot.conversation.Title = tc.title
		slot.conversation.TitleFromRoomProfile = tc.fromProfile
		e.avatarWake = make(chan struct{}, 1)
		e.avatarTargets = map[string]string{room: "room"}
		e.avatarDirty = make(map[string]string)
		if tc.hint != "" {
			e.avatarDirty[room] = tc.hint
		}
		feedRoomPresence(t, m, e, `<presence from="room@conference.example.test/Oie" id="join-request"><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/></x></presence>`)
		hint, refresh := e.avatarDirty[room]
		if slot.snapshot.State != "joined" || len(e.avatarWake) != 1 || refresh != tc.wantRefresh || hint != tc.hint {
			t.Fatalf("saved title %q, state %q, refresh %v hint %q", tc.title, slot.snapshot.State, refresh, hint)
		}
	}
}

func TestMemberOnlyDiscoTitleAfterCredentialedJoin(t *testing.T) {
	m, e, slot, _ := roomFixture(t)
	session, conn, peer := reactionWireSession(t)
	e.session, e.raw = session, conn
	// Room discovery after entry also works when no drawer cache slot is free.
	e.details = make(map[domain.ID]*detailSlot)
	for i := range maxDetailConversations {
		e.details[domain.ID(fmt.Sprint(i))] = &detailSlot{}
	}
	m.host = &roomTitleHostStub{}
	done := make(chan struct{})
	go func() { _ = session.Serve(nil); close(done) }()
	t.Cleanup(func() { _ = peer.Close(); <-done })
	if err := peer.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	join := make(chan error, 1)
	go func() {
		_, err := m.JoinInvitedRoom(t.Context(), e.account, slot.conversation, "Reader", "fixture password", slot.snapshot.IntentRevisionID)
		join <- err
	}()
	decoder := xml.NewDecoder(peer)
	var iq stanza.IQ
	if err := decoder.Decode(&iq); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(peer, `<iq xmlns="jabber:client" type="error" from="%s" id="%s"><error><forbidden xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`, iq.To, iq.ID); err != nil {
		t.Fatal(err)
	}
	var presence stanza.Presence
	if err := decoder.Decode(&presence); err != nil {
		t.Fatal(err)
	}
	if err := <-join; err != nil {
		t.Fatal(err)
	}
	feedRoomPresence(t, m, e, fmt.Sprintf(`<presence xmlns="jabber:client" from="%s" id="%s"><x xmlns="http://jabber.org/protocol/muc#user"><status code="110"/></x></presence>`, presence.To, presence.ID))
	var query struct {
		stanza.IQ
		Info *struct{} `xml:"http://jabber.org/protocol/disco#info query"`
	}
	if err := decoder.Decode(&query); err != nil {
		t.Fatal(err)
	}
	if query.Info == nil || query.Type != stanza.GetIQ || query.To.String() != slot.conversation.Address {
		t.Fatalf("unexpected postjoin discovery IQ: %+v", query)
	}
	if _, err := fmt.Fprintf(peer, `<iq xmlns="jabber:client" type="result" from="%s" id="%s"><query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text" name="Members’ reading room"/></query></iq>`, query.To, query.ID); err != nil {
		t.Fatal(err)
	}
	m.workers.Wait()
	if title := m.host.(*roomTitleHostStub).title; title != "Members’ reading room" {
		t.Fatalf("joined room title %q", title)
	}
}
