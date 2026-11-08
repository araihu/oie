package xmpp

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/jid"
)

func TestAvatarHashVerificationAndBounds(t *testing.T) {
	data := []byte("protocol digest verification")
	sum := sha1.Sum(data)
	hash := hex.EncodeToString(sum[:])
	decoded, got, err := decodeAvatar("\n"+base64.StdEncoding.EncodeToString(data)+"\n", hash)
	if err != nil || string(decoded) != string(data) || got != hash {
		t.Fatal("valid digest failed", err)
	}
	for _, s := range []string{base64.StdEncoding.EncodeToString(data), "invalid", strings.Repeat("A", (2<<20)+1)} {
		if _, _, err = decodeAvatar(s, strings.Repeat("0", 40)); err == nil {
			t.Fatal("invalid avatar accepted")
		}
	}
}

func TestOwnPEPNotificationsAndCapabilityDiscovery(t *testing.T) {
	e := &entry{account: domain.Account{Address: "alice@localhost"}, avatarWake: make(chan struct{}, 1), avatarTargets: map[string]string{"alice@localhost": "account"}, avatarDirty: map[string]string{}}
	m := &Manager{}
	handle := func(input string) string {
		t.Helper()
		decoder := xml.NewDecoder(strings.NewReader(input))
		token, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		start := token.(xml.StartElement)
		var output bytes.Buffer
		encoder := xml.NewEncoder(&output)
		if err = m.handle(t.Context(), e, tokens{decoder, encoder}, &start); err != nil {
			t.Fatal(err)
		}
		if err = encoder.Flush(); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	handle(`<message xmlns="jabber:client"><event xmlns="http://jabber.org/protocol/pubsub#event"><items node="urn:xmpp:avatar:metadata"/></event></message>`)
	if _, ok := e.avatarDirty["alice@localhost"]; !ok {
		t.Fatal("own server PEP notification ignored")
	}
	caps := avatarCaps()
	node := caps.Node + "#" + caps.Ver
	reply := handle(`<iq xmlns="jabber:client" type="get" id="caps" from="localhost"><query xmlns="http://jabber.org/protocol/disco#info" node="` + node + `"/></iq>`)
	var response struct {
		Info disco.Info `xml:"http://jabber.org/protocol/disco#info query"`
	}
	if err := xml.Unmarshal([]byte(reply), &response); err != nil {
		t.Fatal(err)
	}
	if response.Info.Node != node || response.Info.Hash(sha1.New()) != caps.Ver {
		t.Fatal("capability verification does not match presence", reply)
	}
	if !strings.Contains(reply, avatarMetadataNS+"+notify") {
		t.Fatal("avatar notifications not advertised")
	}
}

func TestOnlyKnownProfileIdentitiesCanInvalidateAvatar(t *testing.T) {
	e := &entry{avatarWake: make(chan struct{}, 1), avatarTargets: map[string]string{"room@conference.localhost": "room", "bob@localhost": "direct"}, avatarDirty: map[string]string{}}
	m := &Manager{}
	for _, a := range []string{"stranger@localhost", "room@conference.localhost/nickname"} {
		j, _ := jid.Parse(a)
		m.queueAvatar(e, j, "", true)
	}
	if len(e.avatarDirty) != 0 {
		t.Fatal("unknown sender or occupant changed profile")
	}
	j, _ := jid.Parse("bob@localhost/device")
	m.queueAvatar(e, j, "", true)
	if _, ok := e.avatarDirty["bob@localhost"]; !ok {
		t.Fatal("known peer update ignored")
	}
	caps := avatarCaps()
	if caps.Ver == "" || caps.Ver != clientInfo().Hash(sha1.New()) {
		t.Fatal("entity caps mismatch")
	}
}
