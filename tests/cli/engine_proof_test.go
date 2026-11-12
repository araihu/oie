package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	adapter "github.com/araihu/oie/internal/adapters/xmpp"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func proofID() string { return uuid.Must(uuid.NewV7()).String() }
func proofJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The fixture owns production storage, protocol sessions, workers and HTTP API.
// Only the external byte provider is deterministic; CLI code only uses HTTP.
type cliProof struct {
	t                    *testing.T
	binary, dir, address string
	roots                *x509.CertPool
	close                func()
	media                *proofMedia
}

func newCLIProof(t *testing.T) *cliProof {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "oiectl")
	cmd := exec.CommandContext(t.Context(), "go", "-C", "../../cli", "build", "-o", binary, "./cmd/oiectl")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, out)
	}
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid development CA")
	}
	p := &cliProof{t: t, binary: binary, dir: t.TempDir(), roots: roots, media: &proofMedia{data: []byte("Oiê CLI bytes\n0123456789")}}
	p.start()
	t.Cleanup(func() {
		if p.close != nil {
			p.close()
		}
	})
	return p
}
func (p *cliProof) start() {
	t := p.t
	ctx, cancel := context.WithCancel(t.Context())
	store, err := sqlite.Open(ctx, p.dir, domain.ID(proofID()))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := application.New(store, "local", func() (domain.ID, error) { return domain.ID(proofID()), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	manager := adapter.New(filepath.Join(p.dir, "private", "connections"), p.roots)
	engine.SetProtocols(manager)
	engine.SetMediaReader(p.media)
	if err := manager.Start(ctx, engine); err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(engine)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	p.address = server.URL
	var once sync.Once
	p.close = func() {
		once.Do(func() {
			cancel()
			server.Close()
			if err := <-done; err != nil {
				t.Error(err)
			}
			manager.Close()
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		})
	}
}
func (p *cliProof) invoke(input string, args ...string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(p.t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.binary, append([]string{"--server", p.address, "--output", "json", "--timeout", "15s"}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	var out, diagnostics bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	err := cmd.Run()
	return out.Bytes(), diagnostics.String(), err
}
func (p *cliProof) call(out any, input string, args ...string) []byte {
	p.t.Helper()
	data, stderr, err := p.invoke(input, args...)
	// Deliberately omit request bodies: they may contain protocol credentials.
	if err != nil || stderr != "" {
		p.t.Fatalf("CLI %s: %v stderr=%s", strings.Join(args, " "), err, stderr)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			p.t.Fatalf("CLI JSON: %v %s", err, data)
		}
	}
	return data
}
func (p *cliProof) failure(code int, input string, args ...string) string {
	p.t.Helper()
	out, stderr, err := p.invoke(input, args...)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != code || len(out) != 0 || stderr == "" {
		p.t.Fatalf("failure exit=%v stdout=%q stderr=%q", err, out, stderr)
	}
	if strings.Contains(stderr, "local-only") {
		p.t.Fatal("credential diagnostic leak")
	}
	return strings.TrimSpace(stderr)
}
func (p *cliProof) wait(label string, fn func() bool) {
	p.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
	p.t.Fatalf("timed out: %s", label)
}
func (p *cliProof) connection(account, state string) {
	p.wait("connection "+state, func() bool {
		var c engineapi.Connection
		p.call(&c, "", "accounts", "connection", "--account-id", account)
		return string(c.State) == state
	})
}
func (p *cliProof) connect(account, password string) {
	endpoint := os.Getenv("OIE_XMPP_ENDPOINT")
	if endpoint == "" {
		endpoint = "127.0.0.1:5222"
	}
	p.call(nil, proofJSON(map[string]string{"password": password, "endpoint": endpoint}), "accounts", "connect", "--account-id", account, "--body-stdin")
	p.connection(account, "connected")
}
func (p *cliProof) messages(conversation string) engineapi.MessagePage {
	var page engineapi.MessagePage
	p.call(&page, "", "messages", "list", "--conversation-id", conversation, "--limit", "100")
	return page
}

// Raw mode consumes actual generated SSE incrementally; only complete frames
// are applied. Cancellation is expected to exit 3, never fabricate success.
func (p *cliProof) observe(cursor string, match func(engineapi.Event) bool) engineapi.Event {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(p.t.Context(), 15*time.Second)
	defer cancel()
	args := []string{"--server", p.address, "--timeout", "12s", "events", "stream"}
	if cursor != "" {
		args = append(args, "--last-event-id", cursor)
	}
	cmd := exec.CommandContext(ctx, p.binary, args...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		p.t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var frameID, kind string
	var data strings.Builder
	var found engineapi.Event
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				var event engineapi.Event
				if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
					p.t.Error(err)
					break
				}
				if event.Id != frameID || string(event.Type) != kind || !strings.Contains(event.Id, ":") {
					p.t.Error("invalid SSE envelope")
					break
				}
				if match(event) {
					found = event
					break
				}
			}
			frameID = ""
			kind = ""
			data.Reset()
		} else if strings.HasPrefix(line, "id: ") {
			frameID = strings.TrimPrefix(line, "id: ")
		} else if strings.HasPrefix(line, "event: ") {
			kind = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		cancel()
	}
	_, _ = io.Copy(io.Discard, pipe)
	err = cmd.Wait()
	var exit *exec.ExitError
	if found.Id == "" || !errors.As(err, &exit) || exit.ExitCode() != 3 {
		p.t.Fatalf("SSE observation/cancellation: id=%q err=%v stderr=%s", found.Id, err, stderr.String())
	}
	return found
}

type proofMedia struct {
	data     []byte
	calls    atomic.Int32
	truncate atomic.Bool
}

func (m *proofMedia) Open(_ context.Context, _ string, r application.MediaReadRequest) (application.MediaRead, error) {
	m.calls.Add(1)
	total := int64(len(m.data))
	start, end := int64(0), total-1
	partial := r.Range != nil && (r.Condition.ETag == "" || r.Condition.ETag == `"fixture-v1"`)
	if partial {
		var ok bool
		start, end, ok = r.Range.Resolve(total)
		if !ok {
			return application.MediaRead{}, application.MediaFailure{Code: "range_not_satisfiable", Total: total}
		}
	}
	typ := "video/mp4"
	switch r.Format {
	case "txt":
		typ = "text/plain"
	case "pdf":
		typ = "application/pdf"
	}
	data := m.data[start : end+1]
	if m.truncate.Load() && len(data) > 1 {
		data = data[:len(data)-1]
	}
	return application.MediaRead{Body: io.NopCloser(bytes.NewReader(data)), Length: end - start + 1, Total: total, Offset: start, Partial: partial, RangeSupported: true, ETag: `"fixture-v1"`, MediaType: typ}, nil
}

func TestCLIDataOnlyXMPP(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("requires isolated development Prosody and CA; see make test-cli-xmpp")
	}
	p := newCLIProof(t)
	var identity engineapi.Engine
	p.call(&identity, "", "system", "engine")
	p.call(nil, "", "system", "health")
	var alice, bob engineapi.Account
	for _, entry := range []struct {
		address string
		out     *engineapi.Account
	}{{"alice@localhost", &alice}, {"bob@localhost", &bob}} {
		p.call(entry.out, proofJSON(map[string]string{"protocol": "xmpp", "address": entry.address, "label": "CLI proof"}), "accounts", "register", "--idempotency-key", proofID(), "--body-stdin")
	}
	p.connect(alice.Id, "alice-local-only")
	p.connect(bob.Id, "bob-local-only")
	var capabilities engineapi.Capabilities
	p.call(&capabilities, "", "accounts", "capabilities", "--account-id", alice.Id)
	if capabilities.DirectText == nil || !*capabilities.DirectText {
		t.Fatal("no actual direct text capability")
	}
	p.call(nil, "", "accounts", "contacts", "--account-id", alice.Id)
	var chat engineapi.Conversation
	p.call(&chat, proofJSON(map[string]string{"accountId": alice.Id, "address": "bob@localhost"}), "conversations", "open", "--idempotency-key", proofID(), "--body-stdin")
	marker := "CLI-proof-" + proofID()
	body := proofJSON(map[string]string{"originalText": marker + " Olá 😀"})
	key := proofID()
	args := []string{"messages", "send", "--conversation-id", chat.Id, "--idempotency-key", key, "--body-stdin"}
	var sent engineapi.Message
	original := p.call(&sent, body, args...)
	if sent.SendState != "queued" {
		t.Fatal("acceptance was not persisted queued intent")
	}
	if replay := p.call(nil, body, args...); !bytes.Equal(original, replay) {
		t.Fatal("replay changed acceptance")
	}
	assertConflictProblem(t, p.failure(4, proofJSON(map[string]string{"originalText": "changed"}), args...),
		"idempotency_conflict", "Idempotency key conflict")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			out, diagnostics, err := p.invoke(body, args...)
			if err != nil || diagnostics != "" || !bytes.Equal(out, original) {
				t.Errorf("concurrent replay: %v %s", err, diagnostics)
			}
		})
	}
	wg.Wait()
	// Observe the actual provider receipt in the independently addressed Bob account.
	var bobChat string
	p.wait("actual recipient message", func() bool {
		var chats engineapi.ConversationPage
		p.call(&chats, "", "conversations", "list", "--account-id", bob.Id)
		for _, c := range chats.Items {
			if c.Address == "alice@localhost" {
				bobChat = c.Id
				if c.Request != nil {
					if *c.Request != "message" {
						return false
					}
					p.call(nil, proofJSON(map[string]any{"accountId": bob.Id, "expectedRevisionId": c.RevisionId, "acceptRequest": true}), "conversations", "preferences", "set", "--conversation-id", c.Id, "--idempotency-key", proofID(), "--body-stdin")
				}
				for _, m := range p.messages(c.Id).Items {
					if m.OriginalText == marker+" Olá 😀" && !m.IsOwn {
						return true
					}
				}
			}
		}
		return false
	})
	event := p.observe(identity.EventCursor, func(e engineapi.Event) bool {
		return e.Conversation != nil && e.Conversation.Id == bobChat && e.Conversation.LastMessage == marker+" Olá 😀" && e.Conversation.Request == nil
	})
	// Reply through the CLI, producing a second actual provider direction.
	var back engineapi.Message
	p.call(&back, proofJSON(map[string]string{"originalText": marker + " reply"}), "messages", "send", "--conversation-id", bobChat, "--idempotency-key", proofID(), "--body-stdin")
	resumed := p.observe(event.Id, func(e engineapi.Event) bool {
		return e.Message != nil && e.Message.OriginalText == marker+" reply" && !e.Message.IsOwn
	})
	if resumed.Id == event.Id {
		t.Fatal("SSE resume repeated applied event")
	}
	// Lose a real committed success response, then reconcile and replay via CLI.
	target, _ := url.Parse(p.address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	lostKey := proofID()
	var dropped atomic.Bool
	proxy.ModifyResponse = func(res *http.Response) error {
		if res.Request.Header.Get("Idempotency-Key") == lostKey && !dropped.Swap(true) {
			_ = res.Body.Close()
			return errors.New("discard committed response")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
	lossy := httptest.NewServer(proxy)
	defer lossy.Close()
	lostArgs := []string{"messages", "send", "--conversation-id", chat.Id, "--idempotency-key", lostKey, "--body-stdin"}
	lostBody := proofJSON(map[string]string{"originalText": marker + " lost"})
	direct := p.address
	p.address = lossy.URL
	p.failure(3, lostBody, lostArgs...)
	p.address = direct
	if !dropped.Load() {
		t.Fatal("response was not actually dropped")
	}
	var receipt engineapi.Operation
	p.call(&receipt, "", "operations", "get", "--operation-id", lostKey)
	var recovered engineapi.Message
	p.call(&recovered, lostBody, lostArgs...)
	if receipt.ResourceId != recovered.Id {
		t.Fatal("lost acceptance reconciliation changed message")
	}
	p.wait("one recipient effect after lost response", func() bool {
		count := 0
		for _, m := range p.messages(bobChat).Items {
			if m.OriginalText == marker+" lost" {
				count++
			}
		}
		if count > 1 {
			t.Fatal("duplicate recipient effect")
		}
		return count == 1
	})
	// Disconnect first so stale revision and queued cancellation are deterministic.
	p.call(nil, "", "accounts", "disconnect", "--account-id", alice.Id)
	p.connection(alice.Id, "not_connected")
	var queued engineapi.Message
	queueKey := proofID()
	queueBody := proofJSON(map[string]string{"originalText": marker + " cancelled"})
	p.call(&queued, queueBody, "messages", "send", "--conversation-id", chat.Id, "--idempotency-key", queueKey, "--body-stdin")
	cancelArgs := []string{"messages", "outbox", "cancel", "--message-id", queued.Id, "--idempotency-key", proofID(), "--body-stdin"}
	assertConflictProblem(t, p.failure(4, proofJSON(map[string]string{"expectedRevisionId": proofID()}), cancelArgs...),
		"state_conflict", "State conflict")
	cancelArgs[len(cancelArgs)-2] = proofID()
	p.call(&queued, proofJSON(map[string]string{"expectedRevisionId": queued.RevisionId}), cancelArgs...)
	if queued.SendState != "cancelled" {
		t.Fatal("queued cancellation failed")
	}
	// Shared bytes are an injected deterministic external provider, never UI routes.
	for _, extension := range []string{"mp4", "txt", "pdf", "docx"} {
		p.call(nil, proofJSON(map[string]string{"originalText": "https://files.example/" + marker + "." + extension}), "messages", "send", "--conversation-id", chat.Id, "--idempotency-key", proofID(), "--body-stdin")
	}
	var library engineapi.SharedContentPage
	byteFailuresChecked := false
	for _, kind := range []string{"video", "documents"} {
		p.wait("shared-content indexing", func() bool {
			p.call(&library, "", "shared", "content", "list", "--type", kind, "--account-id", alice.Id)
			return library.Indexing.State == "complete" && len(library.Items) > 0
		})
		for _, item := range library.Items {
			if !strings.Contains(item.Url, marker) {
				continue
			}
			download := []string{"shared", "content", "download", "--item-id", item.Id, "--output", "raw"}
			if strings.HasSuffix(item.Url, ".docx") {
				p.failure(4, "", download...)
				continue
			}
			bytesOut := p.call(nil, "", download...)
			if !bytes.Equal(bytesOut, p.media.data) {
				t.Fatalf("CLI bytes changed: %q", bytesOut)
			}
			if !byteFailuresChecked {
				limited, stderr, err := p.invoke("", append(download, "--max-output-bytes", "4")...)
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 3 || !bytes.Equal(limited, p.media.data[:4]) || stderr == "" {
					t.Fatalf("bounded byte output: %v %q %s", err, limited, stderr)
				}
				p.media.truncate.Store(true)
				truncated, stderr, err := p.invoke("", download...)
				p.media.truncate.Store(false)
				if !errors.As(err, &exit) || exit.ExitCode() != 3 || len(truncated) >= len(p.media.data) || stderr == "" {
					t.Fatalf("truncated source: %v bytes=%d stderr=%s", err, len(truncated), stderr)
				}
				byteFailuresChecked = true
			}
			ranged := p.call(nil, "", append(download, "--range", "bytes=0-3", "--if-range", `"fixture-v1"`)...)
			if !bytes.Equal(ranged, p.media.data[:4]) {
				t.Fatal("conditional range changed bytes")
			}
			fallback := p.call(nil, "", append(download, "--range", "bytes=0-3", "--if-range", `"changed"`)...)
			if !bytes.Equal(fallback, p.media.data) {
				t.Fatal("If-Range mismatch did not return complete representation")
			}
			p.failure(4, "", append(download, "--range", "bytes=99999-")...)
		}
	}
	var search engineapi.MessageSearchPage
	p.call(&search, "", "messages", "search", "--q", marker, "--account-id", alice.Id)
	if len(search.Items) == 0 {
		t.Fatal("no retained CLI search")
	}
	// Restart same production persistence/protocol adapters and reconcile via CLI.
	p.close()
	p.start()
	var restarted engineapi.Engine
	p.call(&restarted, "", "system", "engine")
	if restarted.Id != identity.Id {
		t.Fatal("restart changed engine identity")
	}
	p.call(&receipt, "", "operations", "get", "--operation-id", lostKey)
	p.call(&recovered, lostBody, lostArgs...)
	p.call(&queued, "", "messages", "get", "--message-id", queued.Id)
	if queued.SendState != "cancelled" || receipt.ResourceId != recovered.Id {
		t.Fatal("restart changed receipt/cancellation")
	}
	p.connect(alice.Id, "alice-local-only")
	p.connection(bob.Id, "connected")
	p.wait("queued outbox resumes after restart", func() bool {
		seen := map[string]int{}
		for _, m := range p.messages(bobChat).Items {
			if strings.Contains(m.OriginalText, "https://files.example/"+marker+".") {
				seen[m.OriginalText]++
			}
		}
		for _, count := range seen {
			if count != 1 {
				t.Fatal("queued intent duplicated after restart")
			}
		}
		return len(seen) == 4
	})
	replayedEvent := p.observe(resumed.Id, func(e engineapi.Event) bool { return e.Message != nil && e.Message.Id == recovered.Id })
	if !strings.HasPrefix(replayedEvent.Id, identity.Id+":") {
		t.Fatal("event cursor lost engine scope after restart")
	}
	p.wait("retained history after restart", func() bool { return len(p.messages(chat.Id).Items) >= 7 })
	for _, m := range p.messages(bobChat).Items {
		if m.OriginalText == marker+" cancelled" {
			t.Fatal("cancelled message reached recipient")
		}
	}
	received := map[string]int{}
	for _, m := range p.messages(bobChat).Items {
		received[m.OriginalText]++
	}
	if received[marker+" Olá 😀"] != 1 || received[marker+" lost"] != 1 {
		t.Fatalf("recipient duplicate after restart: first=%d lost=%d", received[marker+" Olá 😀"], received[marker+" lost"])
	}
	count := 0
	for _, m := range p.messages(chat.Id).Items {
		if m.OriginalText == marker+" lost" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("lost request logical sends after restart: %d", count)
	}
	p.failure(2, "", "messages", "get", "--message-id", "invalid")
	p.failure(4, "", "messages", "get", "--message-id", proofID())
	p.failure(3, "", "events", "stream", "--output", "raw", "--last-event-id", restarted.EventCursor, "--timeout", "1ns")
	t.Logf("engine=%s send=%s recipient=%s recovered=%s resumed=%s bytesProviderCalls=%d; actual bidirectional XMPP, no BFF", identity.Id, sent.Id, bobChat, recovered.Id, resumed.Id, p.media.calls.Load())
}
