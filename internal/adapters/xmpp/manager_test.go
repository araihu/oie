package xmpp

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/xml"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stream"
)

func TestDevelopmentStreamResumption(t *testing.T) {
	if os.Getenv("OIE_XMPP_INTEGRATION") != "1" {
		t.Skip("requires development Prosody with stream management enabled")
	}
	pem, err := os.ReadFile(os.Getenv("OIE_XMPP_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid development CA")
	}
	manager := New(t.TempDir(), roots)
	if err := manager.Start(t.Context(), &hostStub{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	account := domain.Account{
		ID: "019a5df0-0000-7000-8000-000000000001", Protocol: "xmpp", Address: "alice@localhost",
	}
	_, err = manager.Configure(t.Context(), account, application.ConnectionInput{
		Endpoint: os.Getenv("OIE_XMPP_ENDPOINT"), Password: "alice-local-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.RLock()
	e := manager.entries[account.ID]
	manager.mu.RUnlock()
	awaitSession := func(previous *mx.Session) (*mx.Session, net.Conn) {
		t.Helper()
		limit := time.Now().Add(20 * time.Second)
		for time.Now().Before(limit) {
			e.mu.RLock()
			current, transport, state := e.session, e.raw, e.state
			e.mu.RUnlock()
			if current != nil && current != previous && transport != nil && state.State == "connected" {
				return current, transport
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("Oiê did not establish an XMPP connection")
		return nil, nil
	}

	initial, raw := awaitSession(nil)
	if status := initial.StreamManagement(); !status.Resumable || status.Resumed {
		t.Fatalf("fresh connection did not negotiate resumption: %+v", status)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, _ := awaitSession(initial)
	if status := resumed.StreamManagement(); !status.Resumed || !resumed.LocalAddr().Equal(initial.LocalAddr()) {
		t.Fatalf("Oiê did not resume its original resource: %+v, %v", status, resumed.LocalAddr())
	}
}

type hostStub struct {
	received []application.Incoming
	rejected []application.Rejected
}

func (h *hostStub) Account(context.Context, domain.ID) (domain.Account, error) {
	return domain.Account{}, nil
}
func (h *hostStub) Receive(_ context.Context, in application.Incoming) error {
	h.received = append(h.received, in)
	return nil
}
func (h *hostStub) Reject(_ context.Context, in application.Rejected) error {
	h.rejected = append(h.rejected, in)
	return nil
}
func (h *hostStub) ConnectionChanged(context.Context, domain.Connection) error { return nil }

type tokens struct {
	*xml.Decoder
	*xml.Encoder
}

func TestIncomingMessageAndServerRejectionMapping(t *testing.T) {
	host := &hostStub{}
	m := New(t.TempDir(), nil)
	m.host = host
	e := &entry{account: domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "bob@localhost"}}
	inputs := []string{`<message xmlns="jabber:client" from="alice@localhost/phone" to="bob@localhost/oie" type="chat" id="one"><body>Olá &amp; &lt; &gt; 👋</body></message>`, `<message xmlns="jabber:client" from="alice@localhost" type="error" id="two"><body>failed</body><error type="cancel"><service-unavailable xmlns="urn:ietf:params:xml:ns:xmpp-stanzas"/></error></message>`}
	for _, input := range inputs {
		decoder := xml.NewDecoder(strings.NewReader(input))
		start, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		element := start.(xml.StartElement)
		var buf bytes.Buffer
		if err = m.handle(t.Context(), e, tokens{decoder, xml.NewEncoder(&buf)}, &element); err != nil {
			t.Fatal(err)
		}
	}
	if len(host.received) != 1 || host.received[0].OriginalText != "Olá & < > 👋" || host.received[0].Address != "alice@localhost" || host.received[0].IsOwn {
		t.Fatalf("incoming mapping: %+v", host.received)
	}
	if len(host.rejected) != 1 || host.rejected[0].MessageID != "two" || host.rejected[0].FromServer {
		t.Fatalf("rejection scope: %+v", host.rejected)
	}
}

func TestCredentialsAreNotSentWithoutTLS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	captured := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			captured <- ""
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		var buf [8192]byte
		n, _ := conn.Read(buf[:])
		text := string(buf[:n])
		_, _ = io.WriteString(conn, `<stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" from="localhost" version="1.0"><stream:features><mechanisms xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><mechanism>PLAIN</mechanism></mechanisms></stream:features>`)
		n, _ = conn.Read(buf[:])
		captured <- text + string(buf[:n])
	}()
	m := New(t.TempDir(), nil)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	session, raw, err := m.connect(ctx, &entry{account: domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@localhost"}, config: configuration{Endpoint: listener.Addr().String(), Password: "never-send-without-TLS"}}, new(mx.StreamManagement))
	if err == nil {
		_ = raw.Close()
		_ = session.Conn().Close()
		t.Fatal("cleartext session accepted")
	}
	if data := <-captured; strings.Contains(data, "<auth") || strings.Contains(data, "never-send-without-TLS") {
		t.Fatal("credentials attempted without TLS")
	}
}

// The condition is determined from the SASL element, independently of the
// server's human-readable error text.
type saslFailureXML string

func (saslFailureXML) Error() string { return "provider message" }
func (s saslFailureXML) TokenReader() xml.TokenReader {
	return xml.NewDecoder(strings.NewReader(string(s)))
}

func TestAuthenticationFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		condition, code string
		stopped         bool
	}{
		{"temporary-auth-failure", "connection_failed", false},
		{"not-authorized", "authentication_failed", true},
	} {
		t.Run(tc.condition, func(t *testing.T) {
			failure := saslFailureXML(`<failure xmlns="urn:ietf:params:xml:ns:xmpp-sasl"><` + tc.condition + `/><text>Temporary service problem</text></failure>`)
			code, stopped := connectionError(authenticationError{failure})
			if code != tc.code || stopped != tc.stopped {
				t.Fatalf("failure classified as %s, stopped = %v", code, stopped)
			}
		})
	}
}

func TestAuthenticationTransportFailuresRetry(t *testing.T) {
	decoder := xml.NewDecoder(strings.NewReader(`<stream><challenge>`))
	for {
		if _, err := decoder.Token(); err != nil {
			code, stopped := connectionError(authenticationError{err})
			if code != "connection_failed" || stopped {
				t.Fatalf("truncated SASL exchange reported %s, stopped = %v (error: %v)", code, stopped, err)
			}
			break
		}
	}
	for _, failure := range []stream.Error{stream.ConnectionTimeout, stream.Reset, stream.ResourceConstraint, stream.SystemShutdown} {
		t.Run(failure.Error(), func(t *testing.T) {
			code, stopped := connectionError(authenticationError{failure})
			if code != "connection_failed" || stopped {
				t.Fatalf("stream error during SASL reported %s, stopped = %v", code, stopped)
			}
		})
	}
}

func TestConfigureRestartsOnlyStoppedConnection(t *testing.T) {
	m := New(t.TempDir(), nil)
	if err := m.Start(t.Context(), &hostStub{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	account := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@localhost", Protocol: "xmpp"}
	in := application.ConnectionInput{Password: "fixture", Endpoint: "127.0.0.1:1"}
	stopped := &entry{config: configuration{AccountID: account.ID, Password: in.Password, Endpoint: in.Endpoint, Enabled: true}, workerStopped: true}
	m.entries[account.ID] = stopped
	if _, err := m.Configure(t.Context(), account, in); err != nil {
		t.Fatal(err)
	}
	running := m.entries[account.ID]
	if running == stopped {
		t.Fatal("unchanged settings did not restart the stopped worker")
	}
	if _, err := m.Configure(t.Context(), account, in); err != nil || m.entries[account.ID] != running {
		t.Fatalf("unchanged settings replaced the running retry loop: %v", err)
	}
}
