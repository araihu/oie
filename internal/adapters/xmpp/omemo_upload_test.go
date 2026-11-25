package xmpp

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/xmpp/omemo"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	mx "mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

func TestRoomOMEMOActivationWaitsForPlainUpload(t *testing.T) {
	const room = "private@rooms.example.org"
	device, err := omemo.Open(filepath.Join(t.TempDir(), "device"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	scope := roomOperationScope{"test-account", room}
	if admitted, err := m.beginPlainRoomOperation(device, scope); err != nil || !admitted {
		t.Fatal("clear transfer was blocked before activation", err)
	}
	if err := m.saveRoomEncryption(device, scope, true); !errors.Is(err, application.ErrStateConflict) {
		t.Fatal("activation overlapped an authorized plaintext transfer", err)
	}
	m.endPlainRoomOperation(scope)
	if err := m.saveRoomEncryption(device, scope, true); err != nil {
		t.Fatal(err)
	}
	if admitted, err := m.beginPlainRoomOperation(device, scope); err != nil || admitted {
		t.Fatal("clear transfer was allowed after activation", err)
	}
}

func TestOMEMOAttachmentProgressRejectsAnEditedAttachmentOrProtection(t *testing.T) {
	old := domain.StagedAttachment{ID: "first", FileName: "file.png", SHA256: "first hash"}
	p := attachmentUploadProgress{
		service: "upload.example.test", roomIntent: "join", files: []domain.StagedAttachment{old},
		links: []string{"cached encrypted link"}, updatedAt: time.Now(), protected: true,
	}
	out := application.Outgoing{RoomIntentRevisionID: "join", Attachments: []application.OutgoingAttachment{{StagedAttachment: old}}}
	if !p.reusable(out, p.service, true) || p.reusable(out, p.service, false) {
		t.Fatal("cached upload ignored the protection state")
	}
	out.Attachments[0].SHA256 = "replacement hash"
	if p.reusable(out, p.service, true) {
		t.Fatal("an edited message could reuse the superseded upload")
	}
}

func TestOMEMOSensitiveWriteBlocksReplayUntilAcknowledged(t *testing.T) {
	e := &entry{}
	status := mx.StreamManagementStatus{Enabled: true, Acknowledged: 9, Unacknowledged: 2}
	e.recordProtectedSequence(status, mx.ErrStreamManagementQueueFull)
	if e.pendingProtectedSend != nil {
		t.Fatal("definitely unsent message disabled replay")
	}
	e.recordProtectedSequence(status, mx.ErrStreamManagementOutcomeUnknown)
	if !stanzaAwaitingAck(e.pendingProtectedSend, 10) || stanzaAwaitingAck(e.pendingProtectedSend, 11) {
		t.Fatal("resumption guard ignored the sensitive stanza's acknowledgement")
	}
}

func TestRoomOMEMOValidatesAuthorityAfterTakingWriter(t *testing.T) {
	const room = "private@rooms.example.org"
	session, _, _ := contactSession(t, func(stanza.IQ, int) string {
		t.Error("stale stanza reached the provider")
		return ""
	})
	slot := &roomSlot{session: session, snapshot: domain.RoomSession{State: "joined"},
		management: roomManagement{self: roomOccupant{affiliation: "member"}}}
	e := &entry{session: session, state: domain.Connection{State: "connected"},
		rooms: map[string]*roomSlot{room: slot}, roomConfigurations: map[string]roomConfiguration{room: {revision: 1, omemo: true}}}
	authority := roomOMEMOAuthority{slot: slot, revision: 1}
	unit := outboundMessage{Message: stanza.Message{To: jid.MustParse(room), Type: stanza.GroupChatMessage},
		Encrypted: &omemo.Envelope{Header: omemo.Header{From: 1}}}
	writer := session.TokenWriter()
	release := func() { _ = writer.Close() }
	defer release()
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() { result <- e.encodeOutgoingStanza(ctx, session, room, unit, authority) }()
	select {
	case err := <-result:
		t.Fatalf("send returned before the writer was released: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	e.historyMu.Lock()
	e.roomConfigurations[room] = roomConfiguration{revision: 2, pending: true}
	e.historyMu.Unlock()
	release()
	if err := <-result; !errors.Is(err, application.ErrStateConflict) {
		t.Fatalf("outdated authorization allowed after writer wait: %v", err)
	}
}

func TestOMEMOUploadRequiresAuthorizedHTTPSDestination(t *testing.T) {
	const service = "upload.example.test"
	const original = "file contents"
	var putCount atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		putCount.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPut || len(body) != len(original)+omemo.MediaTagSize ||
			string(body) == original || r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("invalid ciphertext upload: %s %d %v", r.Method, len(body), err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	session, _, _ := contactSession(t, func(iq stanza.IQ, request int) string {
		getURL := server.URL + "/file"
		if request == 2 {
			getURL += "#"
		}
		return fmt.Sprintf("<iq xmlns='jabber:client' type='result' id='%s' from='%s'><slot xmlns='%s'><put url='%s/file'/><get url='%s'/></slot></iq>",
			iq.ID, service, uploadNamespace, server.URL, getURL)
	})
	e := &entry{session: session, account: domain.Account{Address: "alice@example.test"}, state: domain.Connection{State: "connected"},
		discovery: domain.CapabilityDiscovery{State: "complete", Services: []domain.DiscoveredService{
			{Address: service, Scope: "service", State: "known", Features: []string{uploadNamespace}},
		}},
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	m := New(t.TempDir(), roots)
	out := application.Outgoing{ID: "message", Attachments: []application.OutgoingAttachment{{
		StagedAttachment: domain.StagedAttachment{FileName: "file.txt", MediaType: "text/plain", Size: int64(len(original))},
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(original)), nil
		},
	}}}
	if _, outcome := m.uploadAttachmentLinks(t.Context(), e, session, out, true); outcome != application.Failed || putCount.Load() != 0 {
		t.Fatalf("unconfigured private destination: %s, PUTs=%d", outcome, putCount.Load())
	}
	if err := m.SetPrivateOMEMOUploadOrigin(server.URL); err != nil {
		t.Fatal(err)
	}
	if links, outcome := m.uploadAttachmentLinks(t.Context(), e, session, out, true); outcome != "" ||
		len(links) != 1 || !strings.HasPrefix(links[0], "aesgcm://") || putCount.Load() != 1 {
		t.Fatalf("configured origin did not receive ciphertext: %s, links=%d, PUTs=%d", outcome, len(links), putCount.Load())
	}
	out.ID = "invalid-slot"
	if _, outcome := m.uploadAttachmentLinks(t.Context(), e, session, out, true); outcome != application.Failed || putCount.Load() != 1 {
		t.Fatalf("slot with empty fragment accepted: %s, PUTs=%d", outcome, putCount.Load())
	}
}

func TestOMEMOUploadWaitsForRecipientDiscoveryBeforeOpeningFile(t *testing.T) {
	const peer = "bob@example.test"
	a := domain.Account{ID: "account", Address: "alice@example.test"}
	device, err := omemo.Open(filepath.Join(t.TempDir(), "device"))
	if err != nil {
		t.Fatal(err)
	}
	session, raw, _ := contactSession(t, func(iq stanza.IQ, request int) string {
		if request != 0 || iq.To.String() != peer {
			t.Error("file upload or key material requested before recipient verification")
		}
		return fmt.Sprintf("<iq xmlns='jabber:client' type='error' id='%s' from='%s'><error type='cancel'><item-not-found xmlns='urn:ietf:params:xml:ns:xmpp-stanzas'/></error></iq>", iq.ID, iq.To.String())
	})
	e := &entry{account: a, session: session, raw: raw, omemoDevice: device, state: domain.Connection{State: "connected"}}
	m := &Manager{entries: map[domain.ID]*entry{a.ID: e}}
	out := application.Outgoing{ID: "message", Kind: "direct", Address: peer, DirectEncryption: "omemo",
		RecordWireAttempt: func(context.Context, string, ...domain.WireMentionProof) error {
			t.Error("unverified send attempted to write")
			return nil
		}, Attachments: []application.OutgoingAttachment{{
			StagedAttachment: domain.StagedAttachment{FileName: "private.txt", MediaType: "text/plain", Size: 7},
			Open: func(context.Context) (io.ReadCloser, error) {
				t.Error("unverified send opened the file")
				return nil, application.ErrInvalid
			},
		}}}
	if got := m.Send(t.Context(), a, out); got != application.ProtectionPending {
		t.Fatalf("recipient discovery failure did not leave the message cancellable: %s", got)
	}
}
