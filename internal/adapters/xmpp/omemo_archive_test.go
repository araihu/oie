package xmpp

import (
	"context"
	"encoding/xml"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

type omemoHistoryHost struct {
	hostStub
}

func (h *omemoHistoryHost) RetainedOMEMO(_ context.Context, in application.Incoming, _ string) (bool, error) {
	for _, received := range h.received {
		if received.Address == in.Address && received.ProviderID == in.ProviderID {
			return true, nil
		}
	}
	return false, nil
}

func TestOMEMOArchiveAuthenticatedCatchupAndReplay(t *testing.T) {
	ctx := t.Context()
	host := &omemoHistoryHost{}
	m := &Manager{host: host}
	account := domain.Account{Address: "alice@localhost", ID: "019a5df0-0000-7000-8000-000000000001"}
	self, err := omemo.Open(filepath.Join(t.TempDir(), "device.json"))
	if err != nil {
		t.Fatal(err)
	}
	query := &archiveQuery{ID: "query", Scope: account.Address, RecoverOMEMO: true}
	e := &entry{account: account, omemoDevice: self, archives: map[string]*archiveQuery{query.ID: query}}
	selfID, bundle, err := self.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	fp, err := bundle.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	makeResult := func(sender, id, text string, trusted bool, preKeyIndex int) (*archiveResult, *omemo.Device) {
		t.Helper()
		peer, err := omemo.Open(filepath.Join(t.TempDir(), "device.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := peer.Verify(account.Address, selfID, fp); err != nil {
			t.Fatal(err)
		}
		if trusted {
			device, fingerprint, err := peer.LocalIdentity()
			if err != nil {
				t.Fatal(err)
			}
			if err := self.Verify(sender, device, fingerprint); err != nil {
				t.Fatal(err)
			}
		}
		// Give each fixture sender a different one-time prekey so this test
		// exercises archive catchup independently of prekey selection.
		recipient := bundle
		recipient.PreKeys = bundle.PreKeys[preKeyIndex : preKeyIndex+1]
		env, err := peer.Encrypt(ctx, []omemo.Target{{JID: account.Address, DeviceID: selfID, Bundle: recipient}}, text)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := xml.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		raw := `<result xmlns="urn:xmpp:mam:2" id="` + id + `" queryid="query"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="2026-11-24T18:00:00Z"/><message xmlns="jabber:client" from="` + sender + `/phone" to="alice@localhost" id="origin" type="chat"><body>Untrusted fallback</body>` + string(wire) + `</message></forwarded></result>`
		var result archiveResult
		if err := xml.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		return &result, peer
	}
	unknown, peer := makeResult("bob@localhost", "blocked", "held for verification", false, 0)
	known, _ := makeResult("eve@localhost", "recoverable", "recovered from the archive", true, 1)
	if err := m.captureArchive(e, stanza.Message{From: jid.MustParse("mallory@localhost")}, unknown); err != nil || len(query.OMEMO) != 0 {
		t.Fatal("accepted an archive result from a foreign sender", err)
	}
	for _, result := range []*archiveResult{unknown, known} {
		if err := m.captureArchive(e, stanza.Message{}, result); err != nil {
			t.Fatal(err)
		}
	}
	if len(host.received) != 0 || len(query.Messages) != 0 || len(query.OMEMO) != 2 {
		t.Fatal("capture must only stage encrypted envelopes")
	}
	page := archivePage{Query: query}
	if gap, err := m.ingestOMEMOArchive(ctx, e, page, time.Now()); err != nil || gap.Start.IsZero() || len(host.received) != 1 {
		t.Fatalf("archive pass did not recover the message after the gap: %v %v %+v", gap, err, host.received)
	}
	got := host.received[0]
	if got.OriginalText != "recovered from the archive" || got.ArchiveID != "recoverable" || !got.Historical || !got.Encrypted {
		t.Fatalf("incorrect recovered message: %+v", got)
	}
	device, fingerprint, err := peer.LocalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Verify("bob@localhost", device, fingerprint); err != nil {
		t.Fatal(err)
	}
	if gap, err := m.ingestOMEMOArchive(ctx, e, page, time.Now()); err != nil || !gap.Start.IsZero() || len(host.received) != 2 {
		t.Fatalf("retry lost a message or duplicated a retained one: %v %v %+v", gap, err, host.received)
	}
}
