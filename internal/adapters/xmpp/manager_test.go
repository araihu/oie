package xmpp

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

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
	defer listener.Close()
	captured := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			captured <- ""
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(2 * time.Second))
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
	session, raw, err := m.connect(ctx, &entry{account: domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@localhost"}, config: configuration{Endpoint: listener.Addr().String(), Password: "never-send-without-TLS"}})
	if err == nil {
		raw.Close()
		session.Conn().Close()
		t.Fatal("cleartext session accepted")
	}
	if data := <-captured; strings.Contains(data, "<auth") || strings.Contains(data, "never-send-without-TLS") {
		t.Fatal("credentials attempted without TLS")
	}
}
