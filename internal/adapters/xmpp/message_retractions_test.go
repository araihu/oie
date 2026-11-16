package xmpp

import (
	"context"
	"errors"
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
			if err = awaitRetraction(ctx, attempt); !errors.Is(err, application.ErrStateConflict) {
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
	if err = awaitRetraction(ctx, attempt); err != nil {
		t.Fatalf("verified room echo did not confirm the request: %v", err)
	}
	last := host.received[len(host.received)-1]
	if last.Retraction == nil || last.Retraction.Scope != attempt.plan.Scope || last.Retraction.RemoteID != source {
		t.Fatalf("verified echo was not recorded: %+v", last)
	}
	if len(e.retractionAttempts) != 0 {
		t.Fatal("confirmed attempt remained in the retry table")
	}
	if err = awaitRetraction(ctx, &retractionAttempt{result: make(chan struct{})}); !errors.Is(err, application.ErrProtocolUnavailable) {
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
