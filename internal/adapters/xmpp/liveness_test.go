package xmpp

import (
	"encoding/xml"
	"errors"
	"io"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"mellium.im/xmpp/stanza"
)

func TestIdlePingClosesUnresponsiveStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, provider := net.Pipe()
		defer func() { _ = engine.Close(); _ = provider.Close() }()
		conn := &activityConn{Conn: engine, lastRead: time.Now()}
		session := reactionTestSession(t, conn)
		go watchConnection(t.Context(), session, conn)
		decoder := xml.NewDecoder(provider)
		decoder.DefaultSpace = stanza.NSClient
		var request struct {
			XMLName xml.Name  `xml:"jabber:client iq"`
			Type    string    `xml:"type,attr"`
			Ping    *struct{} `xml:"urn:xmpp:ping ping"`
		}
		if err := decoder.Decode(&request); err != nil || request.Type != string(stanza.GetIQ) || request.Ping == nil {
			t.Fatalf("idle ping = %+v, %v", request, err)
		}
		// The provider can read but never replies. There is no peer TCP close.
		if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
			t.Fatalf("unresponsive stream did not close: %v", err)
		}
	})
}
