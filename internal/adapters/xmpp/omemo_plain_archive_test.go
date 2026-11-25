package xmpp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

type plaintextDirectArchiveHost struct {
	*testArchiveHost
	application.HistoryHost
	peers       []application.RetentionPeer
	checkpoints map[string]string
}

func (h *plaintextDirectArchiveHost) ArchiveRetentionRevision(context.Context, domain.ID) (int64, error) {
	return 1, nil
}

func (h *plaintextDirectArchiveHost) RetentionDays(context.Context, domain.ID, string, string) (int, error) {
	return 30, nil
}

func (h *plaintextDirectArchiveHost) DirectRetentionPeers(context.Context, domain.ID) ([]application.RetentionPeer, error) {
	return h.peers, nil
}

func (h *plaintextDirectArchiveHost) HistoryCheckpoint(_ context.Context, _ domain.ID, scope string) (string, error) {
	return h.checkpoints[scope], nil
}

func (h *plaintextDirectArchiveHost) CommitHistoryCheckpoint(_ context.Context, _ domain.ID, scope, after string) error {
	h.checkpoints[scope] = after
	return nil
}

func (h *plaintextDirectArchiveHost) HistoryChanged(context.Context, domain.HistoryStatus) error {
	return nil
}

func TestDirectOMEMOArchiveRecoversOnlyCurrentPlaintextPeer(t *testing.T) {
	const account, peer, other = "alice@example.test", "bob@example.test", "eve@example.test"
	const previousPeerCursor = "retention-peer:30:omemo:" + peer
	const plaintextPeerCursor = "retention-peer:30:omemo-plain:" + peer
	at := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	for _, test := range []struct {
		name   string
		revoke bool
	}{{"still opted in", false}, {"re-enabled before import", true}} {
		t.Run(test.name, func(t *testing.T) {
			device := newRoomArchiveTestDevice(t)
			peerDevice := newRoomArchiveTestDevice(t)
			encrypted := roomArchiveTestEnvelope(t, peerDevice, account, device, "Encrypted message")
			verifyRoomArchiveTestSender(t, device, peer, peerDevice)
			if err := device.SetEncryption(peer, false); err != nil {
				t.Fatal(err)
			}
			m, e, base := archiveFixture(t, func(iq testArchiveIQ, request int) string {
				if iq.To.String() != account {
					t.Errorf("direct recovery used %s as archive scope", iq.To)
				}
				record := func(uid, sender, payload string) string {
					return fmt.Sprintf(`<message xmlns="jabber:client" from="%s"><result xmlns="urn:xmpp:mam:2" id="%s" queryid="%s"><forwarded xmlns="urn:xmpp:forward:0"><delay xmlns="urn:xmpp:delay" stamp="%s"/><message xmlns="jabber:client" from="%s" to="%s" id="%s" type="chat">%s</message></forwarded></result></message>`,
						account, uid, iq.Query.ID, at, sender, account, uid, payload)
				}
				var page, first, last string
				switch request {
				case 0:
					if iq.field("with") != peer || iq.Query.Set.After != "" || iq.field("start") == "" {
						t.Errorf("plaintext recovery did not use a new retention-scoped cursor: %+v", iq)
					}
					first, last = "bob-plain", "bob-encrypted"
					page = record(first, peer, "<body>Explicitly allowed plaintext</body>") +
						record("outside-peer", other, "<body>Other peer from same query</body>") +
						record(last, peer, encrypted)
					if test.revoke {
						if err := device.SetEncryption(peer, true); err != nil {
							t.Error(err)
						}
					}
				case 1:
					if iq.field("with") != other {
						t.Errorf("unexpected protected peer scope %s", iq.field("with"))
					}
					first, last = "eve-plain", "eve-plain"
					page = record(first, other, "<body>Protected peer plaintext</body>")
				case 2:
					if iq.field("with") != "" {
						t.Errorf("unexpected account-wide peer scope %s", iq.field("with"))
					}
					first, last = "unscoped-plain", "unscoped-plain"
					page = record(first, peer, "<body>Unscoped plaintext</body>")
				default:
					t.Error("unexpected archive query")
					return iq.page(true)
				}
				return page + fmt.Sprintf(`<iq xmlns="jabber:client" from="%s" type="result" id="%s"><fin xmlns="urn:xmpp:mam:2" complete="true"><set xmlns="http://jabber.org/protocol/rsm"><first>%s</first><last>%s</last></set></fin></iq>`, account, iq.ID, first, last)
			})
			e.omemoDevice = device
			host := &plaintextDirectArchiveHost{testArchiveHost: base,
				peers:       []application.RetentionPeer{{Address: peer, Days: 30}, {Address: other, Days: 30}},
				checkpoints: map[string]string{previousPeerCursor: "old-ciphertext-cursor"},
			}
			m.host = host
			ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
			defer cancel()
			err := m.recoverDirectArchives(ctx, host, e, e.session)
			if test.revoke {
				if !errors.Is(err, application.ErrStateConflict) || len(host.messages) != 0 || host.checkpoints[plaintextPeerCursor] != "" {
					t.Fatalf("a stale opt-in imported data or advanced the checkpoint: %v %+v", err, host.messages)
				}
				return
			}
			if err != nil || len(host.messages) != 2 || host.messages["bob-plain"].OriginalText != "Explicitly allowed plaintext" ||
				host.messages["bob-encrypted"].OriginalText != "Encrypted message" || host.checkpoints[plaintextPeerCursor] != "bob-encrypted" ||
				host.checkpoints[previousPeerCursor] != "old-ciphertext-cursor" {
				t.Fatalf("unexpected historical data or checkpoint: %v %+v %+v", err, host.messages, host.checkpoints)
			}
		})
	}
}
