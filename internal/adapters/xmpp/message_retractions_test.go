package xmpp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
)

func TestDirectRetractionWithoutStreamManagementRequiresAccountArchiveSupport(t *testing.T) {
	m, e, slot, _ := reactionRoomFixture(t)
	c := slot.conversation
	c.Kind, c.Address = "direct", "bob@example.test"
	target := domain.Message{
		AccountID: e.account.ID, ConversationID: c.ID,
		IsOwn: true, Sender: domain.Sender{ID: e.account.ID}, ProviderID: "original",
	}
	if _, ok := m.PrepareRetraction(e.account, c, target); ok {
		t.Fatal("unverified confirmation capabilities enabled a direct retraction")
	}
	service := domain.DiscoveredService{Address: c.Address, Scope: "peer", State: "known", Features: []string{mamNS}}
	e.discovery.Services = []domain.DiscoveredService{service}
	if _, ok := m.PrepareRetraction(e.account, c, target); ok {
		t.Fatal("peer archive evidence does not confirm receipt by the account server")
	}
	e.discovery.Services[0].Scope = "account"
	e.discovery.Services[0].Address = e.account.Address
	if plan, ok := m.PrepareRetraction(e.account, c, target); !ok || plan.RemoteID != target.ProviderID {
		t.Fatalf("verified account archive did not enable direct retraction: %+v %t", plan, ok)
	}
}

func TestTrackedRetractionRejectsOnlyScopedStanzaErrors(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	const command = "retraction-command"
	for _, kind := range []string{"room", "direct"} {
		t.Run(kind, func(t *testing.T) {
			target, acceptedErrorFrom := slot.conversation.Address, slot.conversation.Address
			if kind == "direct" {
				target, acceptedErrorFrom = "bob@remote.example", "bob@remote.example/phone"
			}
			peer, err := jid.Parse(target)
			if err != nil {
				t.Fatal(err)
			}
			attempt := &retractionAttempt{session: e.session, kind: kind, target: peer, result: make(chan struct{})}
			e.retractionAttempts = map[string]*retractionAttempt{command: attempt}
			sendError := func(from, to string) {
				t.Helper()
				feedReaction(t, m, e, `<message xmlns="jabber:client" type="error" from="`+from+`" to="`+to+`" id="`+command+`"><error type="cancel"><forbidden xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></message>`)
			}
			sendError("mallory@remote.example/phone", e.account.Address)
			sendError(acceptedErrorFrom, "other@example.test")
			if kind == "room" {
				sendError(target+"/Bob", e.account.Address)
			}
			if retractionResultReady(attempt) || len(host.rejected) != 0 {
				t.Fatal("foreign error affected the outbound retraction")
			}
			sendError(acceptedErrorFrom, e.account.Address)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err = awaitRetraction(ctx, e.session, attempt); !errors.Is(err, application.ErrStateConflict) {
				t.Fatalf("scoped error did not reject the retraction: %v", err)
			}
			if len(host.rejected) != 0 || len(e.retractionAttempts) != 0 {
				t.Fatal("retraction error was processed as an ordinary message error")
			}
		})
	}
}

func TestRoomRetractionConfirmationRequiresMatchingOwnEcho(t *testing.T) {
	m, e, slot, host := roomFixture(t)
	slot.snapshot.State = "joined"
	room := slot.conversation.Address
	e.occupantScopes = map[string]bool{room: true}
	target, err := jid.Parse(room)
	if err != nil {
		t.Fatal(err)
	}
	const command, own, source = "command", "own-occupant", "original-stanza"
	attempt := &retractionAttempt{
		session: e.session, kind: "room", target: target,
		plan: application.IncomingRetraction{
			Scope: roomCorrectionScope(room, room+"/Oie", own), RemoteID: source,
		},
		result: make(chan struct{}),
	}
	e.retractionAttempts = map[string]*retractionAttempt{command: attempt}
	echo := func(nick, occupant, id, original string) {
		t.Helper()
		feedReaction(t, m, e, `<message xmlns="jabber:client" type="groupchat" from="`+room+"/"+nick+`" id="`+id+`"><occupant-id xmlns="urn:xmpp:occupant-id:0" id="`+occupant+`"/><retract xmlns="urn:xmpp:message-retract:1" id="`+original+`"/></message>`)
	}
	echo("Bob", "other-occupant", command, source)
	echo("Oie", "old-occupant", command, source)
	echo("Oie", own, "another-command", source)
	echo("Oie", own, command, "another-original")
	if retractionResultReady(attempt) {
		t.Fatal("unrelated room retraction confirmed a pending request")
	}
	echo("Oie", own, command, source)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = awaitRetraction(ctx, e.session, attempt); err != nil {
		t.Fatalf("verified room echo did not confirm the request: %v", err)
	}
	last := host.received[len(host.received)-1]
	if last.Retraction == nil || last.Retraction.Scope != attempt.plan.Scope || last.Retraction.RemoteID != source {
		t.Fatalf("verified echo was not recorded: %+v", last)
	}
	if len(e.retractionAttempts) != 0 {
		t.Fatal("confirmed attempt remained in the retry table")
	}
	if err = awaitRetraction(ctx, e.session, &retractionAttempt{result: make(chan struct{})}); !errors.Is(err, application.ErrProtocolUnavailable) {
		t.Fatalf("an unconfirmed request appears successful: %v", err)
	}
}

func retractionResultReady(attempt *retractionAttempt) bool {
	select {
	case <-attempt.result:
		return true
	default:
		return false
	}
}

func TestRetractionRetryFindsOnlyMatchingUnresolvedAttemptForCurrentStream(t *testing.T) {
	now := time.Now()
	session := &mx.Session{}
	plan := application.IncomingRetraction{Scope: "scope", RemoteID: "wire"}
	attempt := &retractionAttempt{session: session, messageID: "message", plan: plan, expiresAt: now.Add(time.Minute)}
	e := &entry{session: session, retractionAttempts: map[string]*retractionAttempt{
		"pending": attempt,
		"expired": {session: session, expiresAt: now},
		"old":     {session: &mx.Session{}, expiresAt: now.Add(time.Minute)},
	}}
	if key, found := e.previousRetractionLocked("message", plan, now); key != "pending" || found != attempt || len(e.retractionAttempts) != 1 {
		t.Fatalf("retry lost its proof or retained expired attempts: %q %p", key, found)
	}
	if _, found := e.previousRetractionLocked("other", plan, now); found != nil {
		t.Fatal("retry reused another message's proof")
	}
}

func TestDirectRetractionReusesReplayAndConfirmedProof(t *testing.T) {
	m, e, slot, _ := reactionRoomFixture(t)
	c := slot.conversation
	c.Kind, c.Address = "direct", "bob@example.test"
	target := domain.Message{
		ID: "message", AccountID: e.account.ID, ConversationID: c.ID,
		IsOwn: true, Sender: domain.Sender{ID: e.account.ID}, ProviderID: "original",
	}
	var management mx.StreamManagement
	initial := connectRetractionStream(t, &management, true, func(peer *retractionStreamPeer) {
		bind := peer.readID("iq")
		peer.write(fmt.Sprintf(`<iq type="result" id="%s"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"><jid>alice@example.test/oie</jid></bind></iq>`, bind))
		peer.readID("enable")
		peer.write(`<enabled xmlns="urn:xmpp:sm:3" resume="true" id="retraction-stream"/>`)
	})
	e.session, e.raw = initial.session, initial.session.Conn()
	served := make(chan error, 1)
	go func() { served <- initial.session.Serve(nil) }()
	plan, ok := m.PrepareRetraction(e.account, c, target)
	if !ok {
		t.Fatal("managed stream did not enable retraction")
	}
	if err := m.SendRetraction(t.Context(), e.account, c, target, plan); !errors.Is(err, application.ErrProtocolUnavailable) {
		t.Fatalf("interrupted write provided an unverified outcome: %v", err)
	}
	if err := <-served; err == nil {
		t.Fatal("managed stream stayed active after interrupted write")
	}
	key, pending := e.previousRetractionLocked(target.ID, plan, time.Now())
	if key == "" || pending == nil || pending.last == nil || *pending.last != 1 {
		t.Fatalf("interrupted managed write lost its stanza proof: %q %+v", key, pending)
	}
	e.suspended, e.session = e.session, nil

	resumed := connectRetractionStream(t, &management, false, func(peer *retractionStreamPeer) {
		peer.readID("resume")
		peer.write(`<resumed xmlns="urn:xmpp:sm:3" previd="retraction-stream" h="0"/>`)
	})
	current, raw := resumed.session, resumed.session.Conn()
	if !current.StreamManagement().Resumed {
		t.Fatal("fixture did not resume its original logical stream")
	}
	// A delayed resume should still track a stanza that Mellium can replay.
	e.resumeRetractionsLocked(e.suspended, current, time.Now().Add(2*retractionRetryWindow))
	e.suspended, e.session, e.raw = nil, current, raw
	go func() { served <- current.Serve(nil) }()
	replayed := resumed.readID("message")
	if replayed != key {
		t.Fatalf("replayed a different retraction: %q, want %q", replayed, key)
	}
	resumed.readID("r")
	resumed.write(`<a xmlns="urn:xmpp:sm:3" h="1"/>`)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for current.StreamManagement().Acknowledged != 1 && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	for range 2 {
		if err := m.SendRetraction(ctx, e.account, c, target, plan); err != nil {
			t.Fatalf("retry failed to reuse confirmation without a second stanza: %v", err)
		}
	}
	if status := current.StreamManagement(); status.Acknowledged != 1 || status.Unacknowledged != 0 {
		t.Fatalf("retry wrote another fallback: %+v", status)
	}
	e.retractionAttempts["unconfirmed"] = &retractionAttempt{session: current, kind: "direct", expiresAt: pending.expiresAt}
	proofExpiry := pending.expiresAt
	e.suspended, e.session = current, nil
	m.discardSuspendedSession(t.Context(), e)
	if len(e.retractionAttempts) != 1 || e.retractionAttempts[key] != pending || pending.expiresAt != proofExpiry {
		t.Fatal("stream replacement altered positive proof or kept unconfirmed wire")
	}
	fresh, freshRaw, _ := reactionWireSession(t)
	e.session, e.raw = fresh, freshRaw
	if err := m.SendRetraction(ctx, e.account, c, target, plan); err != nil {
		t.Fatalf("fresh stream failed to reuse positive proof after local write failure: %v", err)
	}
	if _, found := e.previousRetractionLocked(target.ID, plan, proofExpiry); found != nil {
		t.Fatal("positive proof survived its original expiry")
	}
}

type interruptedRetractionConn struct{ net.Conn }

func (c interruptedRetractionConn) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte(roomRetractionNS)) {
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(data)
}

type retractionStreamPeer struct {
	t       *testing.T
	conn    net.Conn
	decoder *xml.Decoder
	session *mx.Session
}

func (p *retractionStreamPeer) readID(element string) string {
	p.t.Helper()
	token, err := p.decoder.Token()
	start, ok := token.(xml.StartElement)
	if err != nil || !ok || start.Name.Local != element {
		p.t.Fatalf("received %v (%v), want %s", token, err, element)
	}
	var result struct {
		ID string `xml:"id,attr"`
	}
	if err := p.decoder.DecodeElement(&result, &start); err != nil {
		p.t.Fatal(err)
	}
	return result.ID
}

func (p *retractionStreamPeer) write(data string) {
	p.t.Helper()
	if _, err := io.WriteString(p.conn, data); err != nil {
		p.t.Fatal(err)
	}
}

func connectRetractionStream(t *testing.T, management *mx.StreamManagement, interrupt bool, handshake func(*retractionStreamPeer)) *retractionStreamPeer {
	t.Helper()
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
	if err := right.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var client net.Conn = left
	if interrupt {
		client = interruptedRetractionConn{left}
	}
	ready := make(chan struct {
		session *mx.Session
		err     error
	}, 1)
	go func() {
		feature := mx.StartTLS(nil)
		feature.Negotiate = func(_ context.Context, s *mx.Session, _ any) (mx.SessionState, io.ReadWriter, error) {
			return mx.Secure | mx.Authn, s.Conn(), nil
		}
		s, err := management.Connect(t.Context(), jid.MustParse("alice@example.test"), client, feature, mx.BindResource())
		ready <- struct {
			session *mx.Session
			err     error
		}{s, err}
	}()
	reader := bufio.NewReader(right)
	readHeader := func() {
		t.Helper()
		header, err := reader.ReadString('>')
		if strings.HasPrefix(header, "<?xml") && err == nil {
			header, err = reader.ReadString('>')
		}
		if err != nil || !strings.HasPrefix(strings.TrimSpace(header), "<stream:stream") {
			t.Fatalf("stream header %q: %v", header, err)
		}
	}
	const stream = `<stream:stream from="example.test" id="fixture" xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" version="1.0">`
	peer := &retractionStreamPeer{t: t, conn: right, decoder: xml.NewDecoder(reader)}
	readHeader()
	peer.write(stream + `<stream:features><starttls xmlns="urn:ietf:params:xml:ns:xmpp-tls"><required/></starttls></stream:features>`)
	readHeader()
	peer.write(stream + `<stream:features><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind"/><sm xmlns="urn:xmpp:sm:3"/></stream:features>`)
	handshake(peer)
	got := <-ready
	if got.err != nil {
		t.Fatal(got.err)
	}
	peer.session = got.session
	return peer
}
