package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/stream"
)

type failingReceiveHost struct {
	hostStub
	err error
}

func TestManagedSendDefinitivelyUnsent(t *testing.T) {
	for _, cause := range []error{mx.ErrStreamManagementQueueFull, mx.ErrOutputStreamClosed, context.DeadlineExceeded} {
		if !definitivelyUnsent(true, cause) {
			t.Errorf("managed write with %v must be eligible for retry", cause)
		}
		if definitivelyUnsent(true, errors.Join(mx.ErrStreamManagementOutcomeUnknown, cause)) {
			t.Errorf("a write already queued for replay cannot be retried for %v", cause)
		}
	}
	if definitivelyUnsent(false, context.DeadlineExceeded) {
		t.Error("an unmanaged write can time out after partial transmission")
	}
}

func (h *failingReceiveHost) Receive(context.Context, application.Incoming) error {
	return h.err
}

func TestInboundStanzaFailureAndResumption(t *testing.T) {
	transient := errors.New("message store unavailable")
	host := &failingReceiveHost{err: transient}
	manager := &Manager{host: host}
	account := &entry{account: domain.Account{Address: "bob@localhost"}}
	const message = `<message xmlns="jabber:client" from="alice@localhost">`
	for _, tc := range []struct {
		name string
		wire string
		want error
	}{
		{"oversized stanza invalidates the stream", message + "<body>" + strings.Repeat("a", 1<<20) + "</body></message>", stream.PolicyViolation},
		{"invalid JID is discarded", `<message xmlns="jabber:client" from="alice@bad domain"><body>Hello</body></message>`, nil},
		{"storage failure leaves the stanza unhandled", message + "<body>Hello</body></message>", transient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := xml.NewDecoder(strings.NewReader(tc.wire))
			token, err := reader.Token()
			if err != nil {
				t.Fatal(err)
			}
			start := token.(xml.StartElement)
			wire := tokens{Decoder: reader, Encoder: xml.NewEncoder(io.Discard)}
			if got := manager.handle(t.Context(), account, wire, &start); !errors.Is(got, tc.want) {
				t.Fatalf("handle error = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("truncated transport is propagated", func(t *testing.T) {
		reader := xml.NewDecoder(strings.NewReader(message + "<body>"))
		token, err := reader.Token()
		if err != nil {
			t.Fatal(err)
		}
		start := token.(xml.StartElement)
		wire := tokens{Decoder: reader, Encoder: xml.NewEncoder(io.Discard)}
		var syntax *xml.SyntaxError
		if got := manager.handle(t.Context(), account, wire, &start); !errors.As(got, &syntax) || syntax.Msg != "unexpected EOF" {
			t.Fatalf("handle error = %v, want truncated XML", got)
		}
	})
}
