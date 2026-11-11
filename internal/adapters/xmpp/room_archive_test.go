package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
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

func TestArchiveQueuePrioritizesInteractiveWorkByDomain(t *testing.T) {
	m := New(t.TempDir(), nil)
	first, err := m.archiveQueue("one@archive.example.test")
	if err != nil {
		t.Fatal(err)
	}
	same, _ := m.archiveQueue("two@ARCHIVE.example.test")
	other, _ := m.archiveQueue("one@other.example.test")
	if first != same || first == other {
		t.Fatal("wrong archive scheduling scope")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	active, err := first.acquire(ctx, archiveAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan archivePriority, 2)
	for _, priority := range []archivePriority{archiveAutomatic, archiveInteractive} {
		go func() {
			release, err := same.acquire(ctx, priority)
			if err == nil {
				acquired <- priority
				release()
			}
		}()
	}
	for {
		first.mu.Lock()
		waiting := len(first.waiting)
		first.mu.Unlock()
		if waiting == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("requests did not queue")
		case <-time.After(time.Millisecond):
		}
	}
	releaseOther, err := other.acquire(ctx, archiveAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	releaseOther()
	select {
	case <-acquired:
		t.Fatal("a second request acquired the same domain")
	default:
	}
	start := time.Now()
	active()
	select {
	case got := <-acquired:
		if got != archiveInteractive {
			t.Fatal("background work overtook an interactive request")
		}
	case <-ctx.Done():
		t.Fatal("interactive request did not proceed")
	}
	select {
	case <-acquired:
		if time.Since(start) < time.Second {
			t.Fatal("automatic page bypassed the cooldown")
		}
	case <-ctx.Done():
		t.Fatal("automatic work did not resume")
	}
}

type testArchiveHost struct {
	hostStub
	application.RoomArchiveHost
	room       domain.Conversation
	archive    domain.RoomArchive
	messages   map[string]domain.Message
	failInsert bool
	reactions  []application.IncomingReaction
}

func (h *testArchiveHost) RoomArchive(context.Context, domain.ID) (domain.RoomArchive, error) {
	return h.archive, nil
}

func (h *testArchiveHost) StoreRoomArchiveProgress(_ context.Context, _ domain.ID, p domain.RoomArchiveProgress) error {
	h.archive.RoomArchiveProgress = p
	return nil
}

func (h *testArchiveHost) CountRoomArchiveMessages(_ context.Context, _ domain.ID, start, end time.Time) (int, error) {
	n := 0
	for _, message := range h.messages {
		if !message.CreatedAt.Before(start) && !message.CreatedAt.After(end) {
			n++
		}
	}
	return n, nil
}

func (h *testArchiveHost) Receive(_ context.Context, in application.Incoming) error {
	if h.failInsert {
		return errors.New("test write failure")
	}
	if in.Reaction != nil {
		h.reactions = append(h.reactions, *in.Reaction)
		return nil
	}
	h.messages[in.ArchiveID] = domain.Message{
		ID: domain.ID(in.ArchiveID), AccountID: h.room.AccountID, ConversationID: h.room.ID,
		CreatedAt: in.CreatedAt, OriginalText: in.OriginalText, Historical: in.Historical,
	}
	return nil
}

func (h *testArchiveHost) ArchivedRoomMessage(_ context.Context, _ domain.Conversation, id string) (domain.Message, error) {
	if message, ok := h.messages[id]; ok {
		return message, nil
	}
	return domain.Message{}, application.ErrNotFound
}

type testArchiveIQ struct {
	stanza.IQ
	Query struct {
		ID   string `xml:"queryid,attr"`
		Form struct {
			Fields []struct {
				Var   string `xml:"var,attr"`
				Value string `xml:"value"`
			} `xml:"field"`
		} `xml:"jabber:x:data x"`
		Set struct {
			Max    int    `xml:"max"`
			Before string `xml:"before"`
		} `xml:"http://jabber.org/protocol/rsm set"`
	} `xml:"urn:xmpp:mam:2 query"`
}

func (iq testArchiveIQ) field(name string) string {
	for _, f := range iq.Query.Form.Fields {
		if f.Var == name {
			return f.Value
		}
	}
	return ""
}

type testArchiveMessage struct {
	id string
	at time.Time
}

func (iq testArchiveIQ) page(complete bool, messages ...testArchiveMessage) string {
	var wire strings.Builder
	from := iq.To.String()
	for _, m := range messages {
		fmt.Fprintf(&wire, `<message xmlns="jabber:client" from="%s"><result xmlns="urn:xmpp:mam:2" id="%s" queryid="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s/Bob" id="%s" type="groupchat"><body>Fixture message</body></message></forwarded></result></message>`, from, m.id, iq.Query.ID, m.at.Format(time.RFC3339Nano), from, m.id)
	}
	fmt.Fprintf(&wire, `<iq xmlns="jabber:client" from="%s" type="result" id="%s"><fin xmlns="urn:xmpp:mam:2" complete="%t"><set xmlns="http://jabber.org/protocol/rsm">`, from, iq.ID, complete)
	if len(messages) != 0 {
		fmt.Fprintf(&wire, "<first>%s</first><last>%s</last>", messages[0].id, messages[len(messages)-1].id)
	}
	wire.WriteString("</set></fin></iq>")
	return wire.String()
}

func archiveFixture(t *testing.T, reply func(testArchiveIQ, int) string) (*Manager, *entry, *testArchiveHost) {
	t.Helper()
	m, e, slot, _ := roomFixture(t)
	host := &testArchiveHost{
		room: slot.conversation, messages: make(map[string]domain.Message),
		archive: domain.RoomArchive{ConversationID: slot.conversation.ID, Mode: "on_demand"},
	}
	m.host = host
	left, right := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	session, err := mx.NewSession(ctx, jid.MustParse("example.test"), jid.MustParse("alice@example.test/test"), left, mx.Ready, func(context.Context, *stream.Info, *stream.Info, *mx.Session, any) (mx.SessionState, io.ReadWriter, any, error) {
		return mx.Ready, nil, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.session, e.archiveSessionCtx = session, ctx
	var wg sync.WaitGroup
	wg.Go(func() {
		dec := xml.NewDecoder(right)
		for index := 0; ; index++ {
			var iq testArchiveIQ
			if err := dec.Decode(&iq); err != nil {
				return
			}
			if _, err := io.WriteString(right, reply(iq, index)); err != nil {
				return
			}
		}
	})
	wg.Go(func() {
		_ = session.Serve(mx.HandlerFunc(func(tokens xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			return m.handle(ctx, e, tokens, start)
		}))
	})
	t.Cleanup(func() {
		cancel()
		_ = left.Close()
		_ = right.Close()
		wg.Wait()
	})
	return m, e, host
}

func TestRoomArchiveKeepsConfirmedBoundaryAndDeduplicatesReplay(t *testing.T) {
	end := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	start, boundary := end.Add(-48*time.Hour), end.Add(-time.Hour)
	m, e, host := archiveFixture(t, func(iq testArchiveIQ, index int) string {
		switch index {
		case 0, 1:
			if iq.field("start") != start.Format(time.RFC3339) || iq.field("end") != end.Format(time.RFC3339) || iq.Query.Set.Before != "" {
				t.Error("recent archive window changed")
			}
			return iq.page(false, testArchiveMessage{"first", boundary})
		case 2:
			if iq.Query.Set.Before != "first" {
				t.Error("saved cursor not reused")
			}
			return `<iq xmlns="jabber:client" from="` + iq.To.String() + `" type="error" id="` + iq.ID + `"><error type="cancel"><item-not-found xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></iq>`
		case 3:
			if iq.field("start") != start.Format(time.RFC3339) || iq.field("end") != boundary.Format(time.RFC3339) || iq.Query.Set.Before != "" {
				t.Error("cursor fallback did not include confirmed boundary")
			}
			return iq.page(true, testArchiveMessage{"same-timestamp", boundary}, testArchiveMessage{"first", boundary})
		case 4:
			if iq.field("start") != "" || iq.field("end") != start.Format(time.RFC3339) || iq.Query.Set.Before != "" {
				t.Error("older recovery did not start directly before the initial window")
			}
			return iq.page(true, testArchiveMessage{"old", start.Add(-time.Hour)})
		default:
			t.Error("unexpected archive page")
			return iq.page(true)
		}
	})
	p := domain.RoomArchiveProgress{WindowStart: start, WindowEnd: end}
	host.archive.RoomArchiveProgress = p
	host.failInsert = true
	next := func() error { return m.extendRoomArchive(t.Context(), host, e, e.session, host.room, &p) }
	if err := next(); err == nil || host.archive.Cursor != "" {
		t.Fatal("failed page advanced the cursor", err)
	}
	host.failInsert = false
	for range 3 {
		if err := next(); err != nil {
			t.Fatal(err)
		}
	}
	if !host.archive.InitialComplete || !host.archive.OlderComplete || len(host.messages) != 3 || !host.archive.WindowEnd.Equal(end) || !host.archive.WindowStart.Equal(start) {
		t.Fatal("wrong recovered state", host.archive, len(host.messages))
	}
}

func TestRoomArchiveSearchDiscoversCapabilityAndKeepsCoverage(t *testing.T) {
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	m, e, host := archiveFixture(t, func(iq testArchiveIQ, index int) string {
		if index == 0 {
			return `<iq xmlns="jabber:client" from="` + iq.To.String() + `" type="result" id="` + iq.ID + `"><query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="urn:xmpp:mam:2"/><feature var="urn:xmpp:fulltext:0"/></query></iq>`
		}
		if iq.field("{urn:xmpp:fulltext:0}fulltext") != "café & chá" || iq.Query.Set.Max != 50 {
			t.Error("search term or limit missing from XMPP form")
		}
		return iq.page(false, testArchiveMessage{"remote-hit", at})
	})
	result, err := m.RequestRoomArchive(t.Context(), e.account, host.room, domain.RoomArchiveRequest{Action: "search", Query: "café & chá"})
	if err != nil || len(result.Items) != 1 || !result.Items[0].Historical || result.NextCursor == "" || result.Archive.TextSearch != "supported" {
		t.Fatal("expected a retained historical search result", result, err)
	}
	if !host.archive.Boundary.IsZero() || !host.archive.WindowEnd.IsZero() {
		t.Fatal("isolated search advanced recovery progress", host.archive)
	}
	if _, err := m.RequestRoomArchive(t.Context(), e.account, host.room, domain.RoomArchiveRequest{Action: "search", Query: "changed", Cursor: result.NextCursor}); !errors.Is(err, application.ErrInvalid) {
		t.Fatal("search cursor accepted for a different term", err)
	}
}

func TestSemianonymousRoomRecoversRecentArchivedReactionsAfterJoin(t *testing.T) {
	at := time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)
	cutoff := at.Add(time.Minute)
	queries := make(chan testArchiveIQ, 4)
	m, e, host := archiveFixture(t, func(iq testArchiveIQ, _ int) string {
		queries <- iq
		page := iq.page(true, testArchiveMessage{id: "peer", at: at},
			testArchiveMessage{id: "own", at: at.Add(time.Second)},
			testArchiveMessage{id: "foreign", at: at.Add(2 * time.Second)},
			testArchiveMessage{id: "ordinary", at: at.Add(3 * time.Second)})
		// The fixtures share nicknames to verify that only the occupant ID
		// distinguishes ownership. The foreign inner sender is never trusted.
		peer := `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="peer-id"/><reactions xmlns="urn:xmpp:reactions:0" id="source"><reaction>❤</reaction></reactions>`
		own := `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="own-id"/><reactions xmlns="urn:xmpp:reactions:0" id="source"/>`
		page = strings.Replace(page, `id="peer" type="groupchat"><body>Fixture message</body>`, `id="peer" type="groupchat">`+peer, 1)
		page = strings.Replace(page, `id="own" type="groupchat"><body>Fixture message</body>`, `id="own" type="groupchat">`+own, 1)
		page = strings.Replace(page, iq.To.String()+`/Bob" id="foreign" type="groupchat"><body>Fixture message</body>`, `other@conference.example.test/Bob" id="foreign" type="groupchat">`+peer, 1)
		return page
	})
	room := host.room.Address
	e.archiveCapabilities = map[string]roomArchiveCapability{room: {mam: true}}
	e.semianonymousRooms = map[string]bool{room: true}
	e.occupantScopes = map[string]bool{room: true}
	slot := e.rooms[room]
	slot.session = e.session
	original := domain.RoomArchiveProgress{WindowStart: at.Add(-time.Hour), WindowEnd: at, CaughtUpThrough: cutoff,
		RecentComplete: true, InitialComplete: true, OlderComplete: true, Availability: "supported", TextSearch: "unsupported"}
	host.archive.RoomArchiveProgress = original
	replay := func() {
		t.Helper()
		if _, err := m.roomArchiveTurn(t.Context(), t.Context(), host, e, e.session, host.room, domain.RoomArchiveRequest{}, archiveAutomatic, cutoff); err != nil {
			t.Fatal(err)
		}
	}
	replay()
	if len(queries) != 0 {
		t.Fatal("replayed before verified self-presence")
	}
	e.mu.Lock()
	slot.snapshot.State, slot.ownOccupantID = "joined", "own-id"
	e.mu.Unlock()
	replay()
	replay()
	if len(queries) != 1 || len(host.messages) != 0 || host.archive.RoomArchiveProgress != original {
		t.Fatal("reaction replay queried repeatedly, reimported text, or moved coverage", len(queries), len(host.messages), host.archive)
	}
	iq := <-queries
	if iq.Query.Set.Max != archivePageSize || iq.field("end") != cutoff.Format(time.RFC3339Nano) {
		t.Fatal("replay was not bounded to the latest archive page", iq)
	}
	if len(host.reactions) != 2 {
		t.Fatal("expected only the two verified reactions", host.reactions)
	}
	peer, own := host.reactions[0], host.reactions[1]
	if peer.IsOwn || !peer.Historical || peer.ActorName != "Bob" || peer.ArchiveID != "peer" || peer.ArchiveScope != room ||
		peer.Actor != roomReactionActor(e.account.ID, room, "peer-id") || peer.Target.Scope != "room:"+room || peer.Target.ID != "source" ||
		!peer.CreatedAt.Equal(at) || len(peer.Emojis) != 1 || peer.Emojis[0] != "❤" ||
		!own.IsOwn || len(own.Emojis) != 0 || own.Actor != roomReactionActor(e.account.ID, room, "own-id") {
		t.Fatal("archive lost identity, timestamp, target or clear semantics", peer, own)
	}
}

func TestDeferredRoomReactionsSurviveCatchupBeforeJoin(t *testing.T) {
	at := time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)
	cutoff := at.Add(time.Minute)
	recent := make([]testArchiveMessage, 50)
	for i := range recent {
		recent[i] = testArchiveMessage{id: fmt.Sprintf("text-%d", i), at: at.Add(time.Duration(i+1) * time.Second)}
	}
	m, e, host := archiveFixture(t, func(iq testArchiveIQ, index int) string {
		if index == 0 || index == 2 {
			messages := []testArchiveMessage{{id: "reaction", at: at}}
			if index == 0 {
				messages = append(messages, recent[:49]...)
			} else if iq.field("start") != at.Format(time.RFC3339Nano) || iq.field("end") != at.Format(time.RFC3339Nano) || iq.Query.Set.Max != archivePageSize {
				t.Error("expected bounded replay of the persisted skipped range")
			}
			page := iq.page(index == 2, messages...)
			payload := `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="peer"/><reactions xmlns="urn:xmpp:reactions:0" id="source"><reaction>❤</reaction></reactions>`
			return strings.Replace(page, `id="reaction" type="groupchat"><body>Fixture message</body>`, `id="reaction" type="groupchat">`+payload, 1)
		}
		if index == 1 {
			return iq.page(true, recent[49:]...)
		}
		return iq.page(true)
	})
	room := host.room.Address
	e.archiveCapabilities = map[string]roomArchiveCapability{room: {mam: true}}
	e.semianonymousRooms = map[string]bool{room: true}
	e.occupantScopes = map[string]bool{room: true}
	slot := e.rooms[room]
	slot.session = e.session
	p := &host.archive.RoomArchiveProgress
	*p = domain.RoomArchiveProgress{WindowStart: at.Add(-time.Hour), WindowEnd: at.Add(-time.Second), CaughtUpThrough: at.Add(-time.Second), RecentComplete: true, InitialComplete: true, OlderComplete: true, Availability: "supported", TextSearch: "unsupported"}
	for i := 0; i < 2; i++ {
		if err := m.catchUpRoomArchive(t.Context(), host, e, e.session, host.room, p, cutoff); err != nil {
			t.Fatal(err)
		}
	}
	if !p.CaughtUpThrough.Equal(cutoff) || len(host.messages) != 50 {
		t.Fatalf("unexpected fixture coverage %v messages=%d", p.CaughtUpThrough, len(host.messages))
	}
	if !p.DeferredReactions.Start.Equal(at) || !p.DeferredReactions.End.Equal(at) || len(host.reactions) != 0 {
		t.Fatalf("expected persisted deferred range without reactions before join, got %+v", p.DeferredReactions)
	}
	e.mu.Lock()
	slot.snapshot.State, slot.ownOccupantID = "joined", "self"
	e.mu.Unlock()
	if _, err := m.roomArchiveTurn(t.Context(), t.Context(), host, e, e.session, host.room, domain.RoomArchiveRequest{}, archiveAutomatic, cutoff); err != nil {
		t.Fatal(err)
	}
	if !host.archive.DeferredReactions.Start.IsZero() {
		t.Fatal("completed replay left a checkpoint")
	}
	if len(host.reactions) != 1 {
		t.Fatalf("text caught up through %v but replay recovered %d reactions", p.CaughtUpThrough, len(host.reactions))
	}
}

type archiveConfigChangeHost struct {
	*testArchiveHost
	beforeSave func()
}

func (h *archiveConfigChangeHost) StoreRoomArchiveProgress(ctx context.Context, id domain.ID, p domain.RoomArchiveProgress) error {
	if h.beforeSave != nil {
		beforeSave := h.beforeSave
		h.beforeSave = nil
		beforeSave()
	}
	return h.testArchiveHost.StoreRoomArchiveProgress(ctx, id, p)
}

func TestRoomArchiveKeepsCheckpointWhenConfigurationChangesBeforeQuery(t *testing.T) {
	at := time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)
	cutoff := at.Add(time.Minute)
	m, e, host := archiveFixture(t, func(iq testArchiveIQ, i int) string {
		payload := `<occupant-id xmlns="urn:xmpp:occupant-id:0" id="peer"/><reactions xmlns="urn:xmpp:reactions:0" id="source"><reaction>❤</reaction></reactions>`
		page := iq.page(true, testArchiveMessage{id: "old-reaction", at: at}, testArchiveMessage{id: "newer-text", at: at.Add(time.Second)})
		return strings.Replace(page, `id="old-reaction" type="groupchat"><body>Fixture message</body>`, `id="old-reaction" type="groupchat">`+payload, 1)
	})
	room := host.room.Address
	e.archiveCapabilities = map[string]roomArchiveCapability{room: {mam: true}}
	e.semianonymousRooms = map[string]bool{room: true}
	e.occupantScopes = map[string]bool{room: true}
	slot := e.rooms[room]
	slot.session, slot.snapshot.State, slot.ownOccupantID = e.session, "joined", "self"
	slot.recentReactionsRecovered = true
	host.archive.RoomArchiveProgress = domain.RoomArchiveProgress{
		WindowStart: at.Add(-time.Hour), WindowEnd: at.Add(-time.Second), CaughtUpThrough: at.Add(-time.Second),
		RecentComplete: true, InitialComplete: true, OlderComplete: true, Availability: "unknown", TextSearch: "unknown",
	}
	// The authenticated notice arrives after roomArchiveSupport validated the
	// previously cached capabilities, while availability is saved before catch-up.
	wrapped := &archiveConfigChangeHost{testArchiveHost: host, beforeSave: func() {
		feedReaction(t, m, e, `<message xmlns="jabber:client" type="groupchat" from="`+room+`"><x xmlns="http://jabber.org/protocol/muc#user"><status code="104"/></x></message>`)
	}}
	m.host = wrapped
	_, err := m.roomArchiveTurn(t.Context(), t.Context(), wrapped, e, e.session, host.room, domain.RoomArchiveRequest{}, archiveAutomatic, cutoff)
	if !currentRoomConfiguration(e, room).pending {
		t.Fatal("fixture failed to invalidate discovery")
	}
	if !errors.Is(err, errArchive) || !host.archive.CaughtUpThrough.Equal(at.Add(-time.Second)) || len(host.messages) != 0 {
		t.Fatalf("pending configuration changed archive progress: %v, %+v", err, host.archive)
	}
	discovery := `<query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="muc_semianonymous"/><feature var="urn:xmpp:occupant-id:0"/><feature var="urn:xmpp:sid:0"/></query>`
	if _, err := m.decodeDiscoInfo(t.Context(), e, e.session, room, discovery, 1); err != nil {
		t.Fatal(err)
	}
	e.archiveCapabilities[room] = roomArchiveCapability{mam: true}
	if _, err := m.roomArchiveTurn(t.Context(), t.Context(), wrapped, e, e.session, host.room, domain.RoomArchiveRequest{}, archiveAutomatic, cutoff); err != nil {
		t.Fatal(err)
	}
	if !host.archive.CaughtUpThrough.Equal(cutoff) || len(host.reactions) != 1 || len(host.messages) != 1 {
		t.Fatal("catch-up did not resume from the original checkpoint", host.archive, host.reactions)
	}
}
