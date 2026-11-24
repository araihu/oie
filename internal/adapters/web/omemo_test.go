package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestOmemoVerificationLinksBindPeerAndProtocol(t *testing.T) {
	d := engineapi.OmemoDevice{Id: 12, Active: true, Fingerprint: strings.Repeat("ab", 32)}
	s := engineapi.OmemoDevices{Address: "alice@example.org", Devices: []engineapi.OmemoDevice{d}}
	for _, legacy := range []bool{true, false} {
		uri := trustURI(s.Address, d, legacy)
		if got, ok := candidateDevice(uri, s); !ok || got.Id != d.Id {
			t.Fatal("correct link rejected", uri)
		}
		for _, bad := range []string{strings.Replace(uri, "alice", "bob", 1), uri + ";distrust=" + d.Fingerprint, strings.Replace(uri, d.Fingerprint, strings.Repeat("cd", 32), 1)} {
			if _, ok := candidateDevice(bad, s); ok {
				t.Fatal("accepted", bad)
			}
		}
	}
	if _, ok := candidateDevice("xmpp:alice@example.org?trust-message;encryption=urn:xmpp:omemo:2;trust="+d.Fingerprint, s); ok {
		t.Fatal("cross-protocol verification")
	}
}

func TestOmemoSettingsRendersShareableQRImages(t *testing.T) {
	state := engineapi.OmemoDevices{Address: "alice@example.org", Devices: []engineapi.OmemoDevice{{Id: 3, Own: true, Active: true, Fingerprint: strings.Repeat("ab", 32)}}}
	var body bytes.Buffer
	if err := omemoSettingsPage(view{}, state, "account", "", nil).Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	if strings.Count(body.String(), `src="data:image/png;base64,`) != 2 {
		t.Fatal("missing verification QR image")
	}
}
