package xmpp_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
	"mellium.im/xmlstream"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func id() domain.ID {
	value, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return domain.ID(value.String())
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("timed out waiting for actual XMPP state")
}
func TestDevelopmentServerMessageExchange(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("run make test-engine-xmpp against development Prosody")
	}
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid development certificate")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store, err := sqlite.Open(ctx, dir, id())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	engine, err := application.New(store, "hosted", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := adapter.New(filepath.Join(dir, "private", "connections"), roots)
	engine.SetProtocols(manager)
	if err = manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	register := func(address string) string {
		t.Helper()
		res, err := client.RegisterAccountWithResponse(ctx, &engineapi.RegisterAccountParams{IdempotencyKey: string(id())}, engineapi.RegisterAccountJSONRequestBody{Protocol: "xmpp", Address: address, Label: address})
		if err != nil || res.JSON201 == nil {
			t.Fatalf("registration failed: %v %#v", err, res)
		}
		return res.JSON201.Id
	}
	alice, bob := register("alice@localhost"), register("bob@localhost")
	proxy := newNetworkProxy(t)
	directEndpoint := xmppEndpoint()
	connect := func(account, password string) {
		t.Helper()
		endpoint := directEndpoint
		if account == alice {
			endpoint = proxy.listener.Addr().String()
		}
		res, err := client.ConfigureAccountConnectionWithResponse(ctx, account, engineapi.ConfigureAccountConnectionJSONRequestBody{Password: &password, Endpoint: &endpoint})
		if err != nil || res.JSON202 == nil {
			t.Fatalf("connection acceptance failed: %v %#v", err, res)
		}
	}
	state := func(account string) string {
		res, err := client.GetAccountConnectionWithResponse(ctx, account)
		if err != nil || res.JSON200 == nil {
			return ""
		}
		return string(res.JSON200.State)
	}
	connect(alice, "alice-local-only")
	connect(bob, "bob-local-only")
	eventually(t, func() bool { return state(alice) == "connected" && state(bob) == "connected" })
	cap, err := client.GetAccountCapabilitiesWithResponse(ctx, alice)
	if err != nil || cap.JSON200 == nil || cap.JSON200.DirectText == nil || !*cap.JSON200.DirectText {
		t.Fatal("connected adapter did not expose actual direct-text availability")
	}
	// The account matrix reads session-local discovery; it never enables missing app operations.
	eventually(t, func() bool {
		evidence, err := client.GetAccountCapabilitiesWithResponse(ctx, alice)
		if err != nil || evidence.JSON200 == nil {
			return false
		}
		state := evidence.JSON200.Discovery.State
		return (state == "complete" || state == "partial") && len(evidence.JSON200.Discovery.Services) >= 2
	})
	discovered, err := client.GetAccountCapabilitiesWithResponse(ctx, alice)
	if err != nil || discovered.JSON200 == nil {
		t.Fatal("discovery projection", err)
	}
	accountSeen, serverSeen := false, false
	for _, service := range discovered.JSON200.Discovery.Services {
		accountSeen = accountSeen || (service.Scope == "account" && service.Address == "alice@localhost" && service.State == "known")
		serverSeen = serverSeen || (service.Scope == "server" && service.Address == "localhost" && service.State == "known")
	}
	if !accountSeen || !serverSeen {
		t.Fatal("actual server/account discovery absent")
	}
	for _, row := range discovered.JSON200.Features {
		if row.Id == "file_upload" && row.AppSupport != "supported" {
			t.Fatal("implemented upload missing from app capabilities")
		}
		if row.Id == "reactions" && row.ServerSupport != "scope_dependent" {
			t.Fatal("peer feature classified as server flag")
		}
	}
	opened, err := client.OpenConversationWithResponse(ctx, &engineapi.OpenConversationParams{IdempotencyKey: string(id())}, engineapi.OpenConversationJSONRequestBody{AccountId: alice, Address: "bob@localhost"})
	if err != nil || (opened.JSON201 == nil && opened.JSON200 == nil) {
		t.Fatalf("conversation: %v %#v", err, opened)
	}
	openedConversation := opened.JSON201
	if openedConversation == nil {
		openedConversation = opened.JSON200
	}
	conversation := openedConversation.Id
	send := func(key, text string) engineapi.Message {
		t.Helper()
		res, err := client.SendMessageWithResponse(ctx, conversation, &engineapi.SendMessageParams{IdempotencyKey: key}, engineapi.SendMessageJSONRequestBody{OriginalText: text})
		if err != nil || res.JSON202 == nil {
			t.Fatalf("send acceptance: %v %#v", err, res)
		}
		return *res.JSON202
	}
	key := string(id())
	text := "Oie TLS exchange " + key + " <&>\nOlá, mundo 👋"
	sent := send(key, text)
	replay, err := client.SendMessageWithResponse(ctx, conversation, &engineapi.SendMessageParams{IdempotencyKey: key}, engineapi.SendMessageJSONRequestBody{OriginalText: text})
	if err != nil || replay.JSON200 == nil || replay.JSON200.Id != sent.Id {
		t.Fatal("HTTP retry did not replay original accepted message")
	}
	var receivedConversation string
	eventually(t, func() bool {
		res, err := client.ListConversationsWithResponse(ctx, &engineapi.ListConversationsParams{AccountId: &bob})
		if err != nil || res.JSON200 == nil {
			return false
		}
		for _, c := range res.JSON200.Items {
			if c.Address == "alice@localhost" {
				if c.Request != nil {
					if *c.Request != "message" {
						return false
					}
					accept := true
					r, err := client.UpdateConversationPreferencesWithResponse(ctx, c.Id, &engineapi.UpdateConversationPreferencesParams{IdempotencyKey: string(id())}, engineapi.UpdateConversationPreferences{AccountId: bob, ExpectedRevisionId: c.RevisionId, AcceptRequest: &accept})
					if err != nil || r.JSON200 == nil {
						return false
					}
				}
				receivedConversation = c.Id
				return true
			}
		}
		return false
	})
	eventually(t, func() bool {
		res, err := client.GetMessageWithResponse(ctx, sent.Id)
		return err == nil && res.JSON200 != nil && res.JSON200.SendState == "submitted"
	})
	countBody := func(items []engineapi.Message, body string) int {
		n := 0
		for _, m := range items {
			if m.OriginalText == body {
				n++
			}
		}
		return n
	}
	eventually(t, func() bool {
		r, err := client.ListMessagesWithResponse(ctx, receivedConversation, nil)
		return err == nil && r.JSON200 != nil && countBody(r.JSON200.Items, text) == 1
	})
	got, err := client.ListMessagesWithResponse(ctx, receivedConversation, nil)
	var received engineapi.Message
	if got != nil && got.JSON200 != nil {
		for _, m := range got.JSON200.Items {
			if m.OriginalText == text {
				received = m
			}
		}
	}
	if err != nil || got.JSON200 == nil || countBody(got.JSON200.Items, text) != 1 || received.IsOwn || received.SendState != "received" {
		t.Fatalf("actual receive lost/duplicated/corrupted body: %v %#v", err, got)
	}
	if received.AccountId != bob || received.Sender.DisplayName != "alice@localhost" {
		t.Fatal("account/sender scope lost")
	}
	// Receive a real protocol reply to the engine's outgoing stanza ID.
	replySession, replyRaw := seedSession(t, ctx, "bob@localhost", "bob-local-only", roots)
	defer func() { _ = replyRaw.Close() }()
	go func() {
		_ = replySession.Serve(mx.HandlerFunc(func(xmlstream.TokenReadEncoder, *xml.StartElement) error { return nil }))
	}()
	toAlice, _ := jid.Parse("alice@localhost")
	replyText := "live reply 🍰 " + string(id())
	if err = replySession.Encode(ctx, struct {
		stanza.Message
		Body  string `xml:"body"`
		Reply struct {
			ID string `xml:"id,attr"`
			To string `xml:"to,attr"`
		} `xml:"urn:xmpp:reply:0 reply"`
	}{stanza.Message{To: toAlice, Type: stanza.ChatMessage, ID: string(id())}, replyText, struct {
		ID string `xml:"id,attr"`
		To string `xml:"to,attr"`
	}{sent.Id, "alice@localhost"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		page, err := client.ListMessagesWithResponse(ctx, conversation, nil)
		if err != nil || page.JSON200 == nil {
			return false
		}
		for _, m := range page.JSON200.Items {
			if m.OriginalText == replyText {
				return m.Reply != nil && m.Reply.Available && m.Reply.MessageId != nil && *m.Reply.MessageId == sent.Id && m.Reply.Excerpt == text && m.Reply.BodyText == replyText
			}
		}
		return false
	})
	window, err := client.GetMessageContextWithResponse(ctx, conversation, sent.Id, nil)
	if err != nil || window.JSON200 == nil || window.JSON200.TargetId != sent.Id {
		t.Fatal("live original context unavailable", err)
	}
	_ = replyRaw.Close()
	// A disconnected owner's send must persist and wait, not claim submission.
	disabled, err := client.DisconnectAccountWithResponse(ctx, alice)
	if err != nil || disabled.JSON200 == nil || disabled.JSON200.State != "not_connected" {
		t.Fatal("disconnect failed")
	}
	cleared, err := client.GetAccountCapabilitiesWithResponse(ctx, alice)
	if err != nil || cleared.JSON200 == nil || cleared.JSON200.Discovery.State != "not_connected" || len(cleared.JSON200.Discovery.Services) != 0 {
		t.Fatal("disconnect exposed old session evidence", err)
	}
	queued := send(string(id()), "Queued until reconnect "+string(id()))
	time.Sleep(100 * time.Millisecond)
	current, err := client.GetMessageWithResponse(ctx, queued.Id)
	if err != nil || current.JSON200 == nil || current.JSON200.SendState != "queued" {
		t.Fatal("offline message was not honestly queued")
	}
	cancelled := send(string(id()), "This send is cancelled")
	cancellation, err := client.CancelMessageWithResponse(ctx, cancelled.Id, &engineapi.CancelMessageParams{IdempotencyKey: string(id())}, engineapi.CancelMessageJSONRequestBody{ExpectedRevisionId: cancelled.RevisionId})
	if err != nil || cancellation.JSON200 == nil || cancellation.JSON200.SendState != "cancelled" {
		t.Fatal("queued cancellation failed")
	}
	connect(alice, "alice-local-only")
	eventually(t, func() bool {
		r, err := client.GetMessageWithResponse(ctx, queued.Id)
		return err == nil && r.JSON200 != nil && r.JSON200.SendState == "submitted"
	})
	eventually(t, func() bool {
		r, err := client.ListMessagesWithResponse(ctx, receivedConversation, nil)
		return err == nil && r.JSON200 != nil && countBody(r.JSON200.Items, queued.OriginalText) == 1
	})
	blocked, err := client.CancelMessageWithResponse(ctx, queued.Id, &engineapi.CancelMessageParams{IdempotencyKey: string(id())}, engineapi.CancelMessageJSONRequestBody{ExpectedRevisionId: queued.RevisionId})
	if err != nil || blocked.StatusCode() != 409 {
		t.Fatal("submitted message cancellation was not rejected")
	}
	// Drop the live TCP path without an API disconnect. Automatic reconnect
	// must resume durable known-unsent work, with no new configure request.
	proxy.cut()
	eventually(t, func() bool { return state(alice) != "connected" })
	networkQueued := send(string(id()), "After network recovery "+string(id()))
	proxy.restore()
	eventually(t, func() bool {
		r, err := client.GetMessageWithResponse(ctx, networkQueued.Id)
		return err == nil && r.JSON200 != nil && r.JSON200.SendState == "submitted"
	})
	eventually(t, func() bool {
		r, err := client.ListMessagesWithResponse(ctx, receivedConversation, nil)
		return err == nil && r.JSON200 != nil && countBody(r.JSON200.Items, networkQueued.OriginalText) == 1
	})
	// A real server rejection is failure evidence, distinct from stream write success.
	invalid, err := client.OpenConversationWithResponse(ctx, &engineapi.OpenConversationParams{IdempotencyKey: string(id())}, engineapi.OpenConversationJSONRequestBody{AccountId: alice, Address: "nonexistent-" + string(id()) + "@localhost"})
	if err != nil || invalid.JSON201 == nil {
		t.Fatal("bad-recipient test setup failed")
	}
	bad, err := client.SendMessageWithResponse(ctx, invalid.JSON201.Id, &engineapi.SendMessageParams{IdempotencyKey: string(id())}, engineapi.SendMessageJSONRequestBody{OriginalText: "Expect a provider rejection"})
	if err != nil || bad.JSON202 == nil {
		t.Fatal("bad-recipient send not accepted")
	}
	lastRejectionState := ""
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("provider rejection final state:", lastRejectionState)
		}
	})
	eventually(t, func() bool {
		r, err := client.GetMessageWithResponse(ctx, bad.JSON202.Id)
		if err == nil && r.JSON200 != nil {
			lastRejectionState = string(r.JSON200.SendState)
		}
		return err == nil && r.JSON200 != nil && r.JSON200.SendState == "failed"
	})
	t.Run("scheduled attachment delivered through HTTP upload", func(t *testing.T) {
		testScheduledAttachmentUpload(t, ctx, client, roots, conversation, receivedConversation)
	})
	// Incorrect authentication must produce a stable non-secret error.
	connect(alice, "incorrect-fixture-password")
	eventually(t, func() bool { return state(alice) == "error" })
	failure, _ := client.GetAccountConnectionWithResponse(ctx, alice)
	if failure.JSON200 == nil || failure.JSON200.LastErrorCode == nil || *failure.JSON200.LastErrorCode != "authentication_failed" {
		t.Fatalf("authentication failure missing stable code: %s", failure.Body)
	}
	events, err := engine.Events(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(events)
	if strings.Contains(string(data), "alice-local-only") || strings.Contains(string(data), "incorrect-fixture-password") {
		t.Fatal("credentials leaked to change stream")
	}
	files, err := os.ReadDir(filepath.Join(dir, "private", "connections"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		info, err := f.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private connection file permissions")
		}
	}
	// Stored history is independent from protocol server retention.
	persisted, err := store.Messages(ctx, domain.ID(receivedConversation), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{text, queued.OriginalText, networkQueued.OriginalText} {
		count := 0
		for _, m := range persisted {
			if m.OriginalText == want {
				count++
			}
		}
		if count != 1 {
			t.Fatal("incoming history missing or duplicated")
		}
	}
}

func testScheduledAttachmentUpload(t *testing.T, ctx context.Context, client *engineapi.ClientWithResponses, roots *x509.CertPool, sender, recipient string) {
	t.Helper()
	content := "Oie attachment " + string(id())
	var body bytes.Buffer
	multipartBody := multipart.NewWriter(&body)
	file, err := multipartBody.CreateFormFile("file", "references.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(file, content); err != nil {
		t.Fatal(err)
	}
	if err = multipartBody.Close(); err != nil {
		t.Fatal(err)
	}
	attachment := string(id())
	staged, err := client.StageMessageAttachmentWithBodyWithResponse(ctx, sender, attachment, multipartBody.FormDataContentType(), &body)
	if err != nil || staged.JSON201 == nil {
		t.Fatalf("stage file: %+v %v", staged, err)
	}
	at := time.Now().Add(2 * time.Second).UTC()
	files := []string{attachment}
	caption := "Scheduled references " + string(id())
	message, err := client.SendMessageWithResponse(ctx, sender, &engineapi.SendMessageParams{IdempotencyKey: string(id())},
		engineapi.SendMessage{OriginalText: caption, ScheduledAt: &at, AttachmentIds: &files})
	if err != nil || message.JSON202 == nil || message.JSON202.ScheduledAt == nil {
		t.Fatalf("schedule file: %+v %v", message, err)
	}
	if current, err := client.GetMessageWithResponse(ctx, message.JSON202.Id); err != nil || current.JSON200 == nil ||
		(time.Now().Before(at) && current.JSON200.SendState != "queued") {
		t.Fatalf("attachment became eligible early: %+v %v", current, err)
	}
	var download string
	eventually(t, func() bool {
		page, err := client.ListMessagesWithResponse(ctx, recipient, nil)
		if err != nil || page.JSON200 == nil {
			return false
		}
		for _, m := range page.JSON200.Items {
			if link, found := strings.CutPrefix(m.OriginalText, caption+"\n"); found {
				download = link
				if m.CreatedAt.Before(at.Add(-time.Millisecond)) {
					t.Fatalf("recipient received the attachment before the schedule: %v < %v", m.CreatedAt, at)
				}
				return strings.HasPrefix(download, "https://")
			}
		}
		return false
	})
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, download, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(data) != content {
		t.Fatalf("recipient could not download exact bytes: %d %v", response.StatusCode, err)
	}
	eventually(t, func() bool {
		retained, err := client.GetMessageWithResponse(ctx, message.JSON202.Id)
		return err == nil && retained.JSON200 != nil && retained.JSON200.SendState == "submitted" &&
			retained.JSON200.OriginalText == caption && retained.JSON200.Attachments != nil && len(*retained.JSON200.Attachments) == 1
	})
}

// This proxy injects a real stream interruption; no protocol callbacks or send
// results are faked. It forwards every byte to the development Prosody server.
type networkProxy struct {
	listener net.Listener
	mu       sync.Mutex
	up       bool
	active   map[net.Conn]struct{}
	wg       sync.WaitGroup
}

func newNetworkProxy(t *testing.T) *networkProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &networkProxy{listener: listener, up: true, active: make(map[net.Conn]struct{})}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer func() { _ = client.Close() }()
				p.mu.Lock()
				up := p.up
				if up {
					p.active[client] = struct{}{}
				}
				p.mu.Unlock()
				if !up {
					return
				}
				defer func() { p.mu.Lock(); delete(p.active, client); p.mu.Unlock() }()
				server, err := net.DialTimeout("tcp", xmppEndpoint(), time.Second)
				if err != nil {
					return
				}
				defer func() { _ = server.Close() }()
				copied := make(chan struct{})
				go func() { _, _ = io.Copy(server, client); _ = server.Close(); close(copied) }()
				_, _ = io.Copy(client, server)
				_ = client.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(func() { _ = p.listener.Close(); p.cut(); p.wg.Wait() })
	return p
}
func (p *networkProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up = false
	for conn := range p.active {
		_ = conn.Close()
	}
}
func (p *networkProxy) restore() { p.mu.Lock(); p.up = true; p.mu.Unlock() }

func TestConnectionConfigurationSurvivesRestartAndTLSIsVerified(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("run make test-engine-xmpp")
	}
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	dir := t.TempDir()
	endpoint := xmppEndpoint()
	start := func(path string, trust *x509.CertPool) (*application.Engine, *sqlite.Store, *adapter.Manager) {
		t.Helper()
		store, err := sqlite.Open(t.Context(), path, id())
		if err != nil {
			t.Fatal(err)
		}
		engine, err := application.New(store, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		manager := adapter.New(filepath.Join(path, "private", "connections"), trust)
		engine.SetProtocols(manager)
		if err = manager.Start(t.Context(), engine); err != nil {
			t.Fatal(err)
		}
		return engine, store, manager
	}
	e, store, manager := start(dir, roots)
	a, err := e.Register(t.Context(), id(), application.RegisterInput{Protocol: "xmpp", Address: "alice@localhost", Label: "Restart"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ConfigureConnection(t.Context(), a.Account.ID, application.ConnectionInput{Password: "alice-local-only", Endpoint: endpoint}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		c, err := e.Connection(t.Context(), a.Account.ID)
		return err == nil && c.State == "connected"
	})
	manager.Close()
	_ = store.Close()
	again, store2, reconnected := start(dir, roots)
	defer func() { _ = store2.Close() }()
	defer reconnected.Close()
	eventually(t, func() bool {
		c, err := again.Connection(t.Context(), a.Account.ID)
		return err == nil && c.State == "connected"
	})
	// A second independent engine with no trusted fixture CA must reject TLS.
	untrusted, badStore, badManager := start(t.TempDir(), x509.NewCertPool())
	defer func() { _ = badStore.Close() }()
	defer badManager.Close()
	bad, err := untrusted.Register(t.Context(), id(), application.RegisterInput{Protocol: "xmpp", Address: "bob@localhost", Label: "Untrusted TLS"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = untrusted.ConfigureConnection(t.Context(), bad.Account.ID, application.ConnectionInput{Password: "bob-local-only", Endpoint: endpoint}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		c, err := untrusted.Connection(t.Context(), bad.Account.ID)
		return err == nil && c.State == "error" && c.LastErrorCode == "tls_failed"
	})
}

func xmppEndpoint() string {
	if endpoint := os.Getenv("OIE_XMPP_ENDPOINT"); endpoint != "" {
		return endpoint
	}
	return "127.0.0.1:5222"
}

func allowFixtureConversation(ctx context.Context, engine *application.Engine, c domain.Conversation) bool {
	if c.Request == "" {
		return true
	}
	if c.Request != domain.MessageRequest || c.DirectHistoryCheck != nil {
		return false
	}
	_, err := engine.UpdateConversationPreferences(ctx, id(), c.ID, application.ConversationPreferencesInput{AccountID: c.AccountID, ExpectedRevisionID: c.RevisionID, AcceptRequest: true})
	return err == nil
}
