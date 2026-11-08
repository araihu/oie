package xmpp

import (
	"encoding/xml"
	"github.com/araihu/oie/internal/domain"
	"io"
	"mellium.im/xmlstream"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
	"testing"
)

func TestArchiveSenderCorrelationAndForwardedScope(t *testing.T) {
	account := domain.Account{ID: "019a5df0-0000-7000-8000-000000000001", Address: "alice@localhost"}
	raw := `<result xmlns="urn:xmpp:mam:2" id="archive-1" queryid="expected"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="2020-01-01T10:00:00Z"/><message xmlns="jabber:client" from="bob@localhost/phone" to="alice@localhost/desktop" id="provider-1" type="chat"><body>retained &amp; original</body></message></forwarded></result>`
	var result archiveResult
	if err := xml.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	in, ok, err := archivedIncoming(account, account.Address, &result)
	if err != nil || !ok || in.OriginalText != "retained & original" || in.Address != "bob@localhost" || in.IsOwn || !in.Historical {
		t.Fatalf("forwarded parse: %+v %v %v", in, ok, err)
	}
	manager := New(t.TempDir(), nil)
	entry := &entry{account: account, archive: &archiveQuery{ID: "expected", Scope: account.Address}}
	bad, _ := jid.Parse("mallory@localhost")
	if err = manager.captureArchive(entry, stanza.Message{From: bad}, &result); err != nil || len(entry.archive.Messages) != 0 {
		t.Fatal("foreign archive accepted")
	}
	result.QueryID = "stale"
	if err = manager.captureArchive(entry, stanza.Message{}, &result); err != nil || len(entry.archive.Messages) != 0 {
		t.Fatal("stale archive accepted")
	}
	result.QueryID = "expected"
	if err = manager.captureArchive(entry, stanza.Message{}, &result); err != nil || len(entry.archive.Messages) != 1 {
		t.Fatal("authenticated archive result lost")
	}
	foreign, _ := jid.Parse("carol@localhost")
	result.Forwarded.Message.To = foreign
	if _, _, err = archivedIncoming(account, account.Address, &result); err == nil {
		t.Fatal("foreign forwarded recipient accepted")
	}
}

func TestBoundedTokensPreservesFinalTokenWithEOF(t *testing.T) {
	end := xml.EndElement{Name: xml.Name{Local: "iq"}}
	reader := &boundedTokens{reader: xmlstream.Token(end), remaining: 32}
	token, err := reader.Token()
	if err != nil || token == nil {
		t.Fatalf("lost final XML token: %v %v", token, err)
	}
	if _, err = reader.Token(); err != io.EOF {
		t.Fatal("expected final EOF")
	}
}
