package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type protocolStub struct {
	mu        sync.Mutex
	connected bool
	outcome   string
	count     int
	entered   chan struct{}
	release   chan struct{}
}

func (p *protocolStub) NormalizeAddress(_ string, address string) (string, error) {
	if !strings.Contains(address, "@") {
		return "", application.ErrInvalid
	}
	return strings.ToLower(address), nil
}
func (p *protocolStub) Configure(ctx context.Context, a domain.Account, in application.ConnectionInput) (domain.Connection, error) {
	return p.Connection(ctx, a)
}
func (p *protocolStub) Disconnect(ctx context.Context, a domain.Account) (domain.Connection, error) {
	p.mu.Lock()
	p.connected = false
	p.mu.Unlock()
	return p.Connection(ctx, a)
}
func (p *protocolStub) Connection(_ context.Context, a domain.Account) (domain.Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := "not_connected"
	if p.connected {
		state = "connected"
	}
	return domain.Connection{AccountID: a.ID, State: state, UpdatedAt: time.Now().UTC()}, nil
}
func (p *protocolStub) Send(ctx context.Context, _ domain.Account, _ application.Outgoing) string {
	p.mu.Lock()
	p.count++
	outcome := p.outcome
	p.mu.Unlock()
	if p.entered != nil {
		select {
		case p.entered <- struct{}{}:
		default:
		}
	}
	if p.release != nil {
		select {
		case <-ctx.Done():
			return application.Unknown
		case <-p.release:
		}
	}
	return outcome
}
func newMessagingEngine(t *testing.T, dir string, p *protocolStub) (*application.Engine, *sqlite.Store, *httptest.Server) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), dir, id())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	e, err := application.New(store, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	e.SetProtocols(p)
	handler, err := httpapi.New(e)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return e, store, server
}
func openTestConversation(t *testing.T, e *application.Engine, address string) domain.Conversation {
	t.Helper()
	a, err := e.Register(t.Context(), id(), application.RegisterInput{Protocol: "xmpp", Address: address, Label: address})
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.OpenConversation(t.Context(), id(), a.Account.ID, "bob@localhost")
	if err != nil {
		t.Fatal(err)
	}
	return c.Conversation
}
func runWorker(t *testing.T, e *application.Engine) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	return func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("worker failed: %v", err)
		}
	}
}
func waitMessage(t *testing.T, e *application.Engine, id domain.ID, state string) domain.Message {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m, err := e.Message(t.Context(), id)
		if err == nil && m.SendState == state {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("message did not become %s", state)
	return domain.Message{}
}
func TestSendAcceptanceReplayAndCancellationAfterRestart(t *testing.T) {
	dir := t.TempDir()
	p := &protocolStub{outcome: application.Submitted}
	e, store, server := newMessagingEngine(t, dir, p)
	c := openTestConversation(t, e, "alice@localhost")
	key := string(id())
	body := `{"originalText":"  preserve original\nOlá & < > 👋  "}`
	status, data := request(t, server.URL, "POST", "/api/v1/conversations/"+string(c.ID)+"/messages", key, body)
	if status != 202 {
		t.Fatalf("accept: %d %s", status, data)
	}
	var first engineapi.Message
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatal(err)
	}
	if first.SendState != "queued" || first.OriginalText != "  preserve original\nOlá & < > 👋  " {
		t.Fatal("acceptance altered text or overstated evidence")
	}
	status, replay := request(t, server.URL, "POST", "/api/v1/conversations/"+string(c.ID)+"/messages", key, body)
	if status != 200 || string(replay) != string(data) {
		t.Fatalf("receipt replay changed: %d %s", status, replay)
	}
	status, _ = request(t, server.URL, "POST", "/api/v1/conversations/"+string(c.ID)+"/messages", key, `{"originalText":"different"}`)
	if status != 409 {
		t.Fatal("operation key accepted a different body")
	}
	for _, invalid := range []string{`{"originalText":"   "}`, `{"originalText":"hi","password":"leak"}`, `{"originalText":"\u0000"}`} {
		status, _ = request(t, server.URL, "POST", "/api/v1/conversations/"+string(c.ID)+"/messages", string(id()), invalid)
		if status != 400 {
			t.Fatalf("invalid message accepted: %s", invalid)
		}
	}
	server.Close()
	store.Close()
	restarted, _, next := newMessagingEngine(t, dir, p)
	cancelKey := string(id())
	cancelBody := `{"expectedRevisionId":"` + first.RevisionId + `"}`
	status, cancelled := request(t, next.URL, "POST", "/api/v1/messages/"+first.Id+"/cancel", cancelKey, cancelBody)
	if status != 200 || !strings.Contains(string(cancelled), `"sendState":"cancelled"`) {
		t.Fatalf("restart cancellation: %d %s", status, cancelled)
	}
	status, replay = request(t, next.URL, "POST", "/api/v1/messages/"+first.Id+"/cancel", cancelKey, cancelBody)
	if status != 200 || string(replay) != string(cancelled) {
		t.Fatal("cancel operation replay changed")
	}
	p.mu.Lock()
	p.connected = true
	p.mu.Unlock()
	stop := runWorker(t, restarted)
	defer stop()
	time.Sleep(40 * time.Millisecond)
	p.mu.Lock()
	count := p.count
	p.mu.Unlock()
	if count != 0 {
		t.Fatal("cancelled intent was dispatched after restart")
	}
}
func TestDispatchCutoffAndUnknownOutcomeNeverBlindlyRetries(t *testing.T) {
	p := &protocolStub{connected: true, outcome: application.Unknown, entered: make(chan struct{}, 1), release: make(chan struct{})}
	e, _, server := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	accepted, err := e.SendMessage(t.Context(), id(), c.ID, "uncertain external side effect")
	if err != nil {
		t.Fatal(err)
	}
	stop := runWorker(t, e)
	select {
	case <-p.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("send did not start immediately")
	}
	status, data := request(t, server.URL, "POST", "/api/v1/messages/"+string(accepted.Message.ID)+"/cancel", string(id()), `{"expectedRevisionId":"`+string(accepted.Message.RevisionID)+`"}`)
	if status != 409 || !strings.Contains(string(data), `"currentMessage"`) || !strings.Contains(string(data), `"sendState":"dispatching"`) {
		t.Fatalf("cutoff lied: %d %s", status, data)
	}
	close(p.release)
	waitMessage(t, e, accepted.Message.ID, "unknown")
	stop()
	stop = runWorker(t, e)
	time.Sleep(60 * time.Millisecond)
	stop()
	p.mu.Lock()
	count := p.count
	p.mu.Unlock()
	if count != 1 {
		t.Fatalf("unknown result blindly retried %d times", count)
	}
}
func TestInterruptedDispatchBecomesUnknownAndQueuedStillResumes(t *testing.T) {
	p := &protocolStub{connected: true, outcome: application.Submitted}
	e, store, _ := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	interrupted, err := e.SendMessage(t.Context(), id(), c.ID, "process stops during a write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.TransitionMessage(t.Context(), interrupted.Message.ID, "queued", "dispatching", id(), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	queued, err := e.SendMessage(t.Context(), id(), c.ID, "ordinary durable queued intent")
	if err != nil {
		t.Fatal(err)
	}
	stop := runWorker(t, e)
	defer stop()
	waitMessage(t, e, interrupted.Message.ID, "unknown")
	waitMessage(t, e, queued.Message.ID, "submitted")
	p.mu.Lock()
	count := p.count
	p.mu.Unlock()
	if count != 1 {
		t.Fatalf("recovery dispatched ambiguous work: %d writes", count)
	}
}
func TestIncomingDedupIsScopedAndConversationActivityUpdates(t *testing.T) {
	p := &protocolStub{}
	e, _, _ := newMessagingEngine(t, t.TempDir(), p)
	c := openTestConversation(t, e, "alice@localhost")
	later := time.Now().UTC().Add(time.Second)
	in := application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "provider-1", OriginalText: "one retained message", CreatedAt: later}
	for range 2 {
		if err := e.Receive(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := e.Messages(t.Context(), c.ID, "", 50)
	if err != nil || len(messages) != 1 {
		t.Fatalf("live replay duplicated: %d %v", len(messages), err)
	}
	current, err := e.Conversation(t.Context(), c.ID)
	if err != nil || current.RevisionID == c.RevisionID || current.UnreadCount != 1 || current.LastMessage != in.OriginalText || !current.UpdatedAt.After(c.UpdatedAt) {
		t.Fatalf("conversation projection not revised: %+v %v", current, err)
	}
	other := openTestConversation(t, e, "work@localhost")
	in.AccountID = other.AccountID
	if err := e.Receive(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	messages, err = e.Messages(t.Context(), other.ID, "", 50)
	if err != nil || len(messages) != 1 || messages[0].AccountID == c.AccountID {
		t.Fatal("provider ID dedup crossed account boundary")
	}
}

func TestIncomingCreatesConversation(t *testing.T) {
	e, _, _ := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	a, err := e.Register(t.Context(), id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Receiver"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Receive(t.Context(), application.Incoming{AccountID: a.Account.ID, Address: "bob@localhost", ProviderID: "stanza-1", OriginalText: "first inbound"}); err != nil {
		t.Fatal(err)
	}
}

func TestIncomingBodyIsNotLimitedByOutgoingComposerLimit(t *testing.T) {
	e, _, _ := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	c := openTestConversation(t, e, "alice@localhost")
	body := strings.Repeat("a", 20000)
	if err := e.Receive(t.Context(), application.Incoming{AccountID: c.AccountID, Address: c.Address, ProviderID: "large-live-body", OriginalText: body}); err != nil {
		t.Fatal(err)
	}
	messages, err := e.Messages(t.Context(), c.ID, "", 50)
	if err != nil || len(messages) != 1 || messages[0].OriginalText != body {
		t.Fatal("incoming original was truncated or dropped")
	}
}
