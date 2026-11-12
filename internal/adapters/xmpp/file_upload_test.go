package xmpp

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
	"mellium.im/xmpp/stanza"
)

func TestUploadServiceUsesAccountServerDiscovery(t *testing.T) {
	const room = "federated@rooms.other.example"
	const service = "upload.example.test"
	roomEvidence := domain.DiscoveredService{Address: room, Scope: "room", State: "known", Features: []string{uploadNamespace}}
	accountEvidence := domain.DiscoveredService{Address: "alice@example.test", Scope: "account", State: "known", Features: []string{uploadNamespace}}
	serviceEvidence := domain.DiscoveredService{Address: service, Scope: "service", State: "known", Features: []string{uploadNamespace}}
	e := &entry{discovery: domain.CapabilityDiscovery{State: "complete", Services: []domain.DiscoveredService{roomEvidence, accountEvidence}}}
	if got, err := uploadService(e, 11); got != "" || !errors.Is(err, application.ErrProtocolUnavailable) {
		t.Fatalf("account or room was accepted as upload service: %q %v", got, err)
	}
	e.discovery.Services = append(e.discovery.Services, serviceEvidence)
	e.uploadLimits = map[string]int64{service: 10}
	if got, err := uploadService(e, 11); got != "" || !errors.Is(err, application.ErrAttachmentTooLarge) {
		t.Fatalf("oversized upload escaped account service limit: %q %v", got, err)
	}
	if got, err := uploadService(e, 10); got != service || err != nil {
		t.Fatalf("account service was not selected: %q %v", got, err)
	}
}

func TestAttachmentUploadRetriesOnlyUnfinishedFiles(t *testing.T) {
	var first, second atomic.Int32
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPut || r.URL.Path != "/"+string(body) {
			t.Errorf("incorrect PUT %s %s %q %v", r.Method, r.URL.Path, body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/first" {
			first.Add(1)
		} else if second.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer endpoint.Close()
	const service = "upload.example.test"
	session, _, _ := contactSession(t, func(iq stanza.IQ, request int) string {
		if iq.To.String() != service {
			t.Errorf("slot request addressed to %s", iq.To)
		}
		name := "first"
		if request != 0 {
			name = "second"
		}
		return fmt.Sprintf("<iq xmlns='jabber:client' type='result' id='%s' from='%s'><slot xmlns='%s'><put url='%s/%s'/><get url='https://%s/%s'/></slot></iq>",
			iq.ID, service, uploadNamespace, endpoint.URL, name, service, name)
	})
	e := &entry{account: domain.Account{Address: "alice@example.test"}, session: session, state: domain.Connection{State: "connected"},
		discovery: domain.CapabilityDiscovery{State: "complete", Services: []domain.DiscoveredService{
			{Address: service, Scope: "service", State: "known", Features: []string{uploadNamespace}},
		}},
	}
	roots := x509.NewCertPool()
	roots.AddCert(endpoint.Certificate())
	manager := New(t.TempDir(), roots)
	out := application.Outgoing{ID: "message", OriginalText: "caption"}
	for _, name := range []string{"first", "second"} {
		out.Attachments = append(out.Attachments, application.OutgoingAttachment{
			StagedAttachment: domain.StagedAttachment{FileName: name, MediaType: "text/plain", Size: int64(len(name))},
			Open:             func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(name)), nil },
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, outcome := manager.uploadMessageAttachments(ctx, e, session, out); outcome != application.NotSent {
		t.Fatal("recoverable PUT did not return a known-unsent outcome", outcome)
	}

	body, outcome := manager.uploadMessageAttachments(ctx, e, session, out)
	if outcome != "" || body != "caption\nhttps://upload.example.test/first\nhttps://upload.example.test/second" {
		t.Fatalf("unexpected completed composition: %q %s", body, outcome)
	}
	if first.Load() != 1 || second.Load() != 2 {
		t.Fatalf("wrong number of PUT attempts: first %d second %d", first.Load(), second.Load())
	}
}

func TestAttachmentOpenCancellationRemainsRetryable(t *testing.T) {
	for _, openErr := range []error{context.Canceled, context.DeadlineExceeded} {
		file := application.OutgoingAttachment{
			Open: func(context.Context) (io.ReadCloser, error) { return nil, openErr },
		}
		if err := sendUpload(t.Context(), nil, uploadSlot{}, file); !errors.Is(err, openErr) {
			t.Fatalf("staged attachment open changed %v into %v", openErr, err)
		}
	}
}

func TestAttachmentUploadStopsWhenConnectionEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	putStarted := make(chan struct{})
	putCanceled := make(chan struct{})
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(putStarted)
		select {
		case <-r.Context().Done():
			close(putCanceled)
		case <-ctx.Done():
		}
	}))
	defer endpoint.Close()
	const service = "upload.example.test"
	session, _, _ := contactSession(t, func(iq stanza.IQ, _ int) string {
		return fmt.Sprintf("<iq xmlns='jabber:client' type='result' id='%s' from='%s'><slot xmlns='%s'><put url='%s'/><get url='https://%s/file'/></slot></iq>",
			iq.ID, service, uploadNamespace, endpoint.URL, service)
	})
	sessionCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	e := &entry{
		account: domain.Account{Address: "alice@example.test"}, session: session, state: domain.Connection{State: "connected"},
		archiveSessionCtx: sessionCtx,
		discovery: domain.CapabilityDiscovery{State: "complete", Services: []domain.DiscoveredService{
			{Address: service, Scope: "service", State: "known", Features: []string{uploadNamespace}},
		}},
	}
	roots := x509.NewCertPool()
	roots.AddCert(endpoint.Certificate())
	manager := New(t.TempDir(), roots)
	out := application.Outgoing{ID: "message", Attachments: []application.OutgoingAttachment{{
		StagedAttachment: domain.StagedAttachment{FileName: "document.txt", MediaType: "text/plain", Size: 4},
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("file")), nil
		},
	}}}
	result := make(chan string, 1)
	go func() {
		_, outcome := manager.uploadMessageAttachments(ctx, e, session, out)
		result <- outcome
	}()
	select {
	case <-putStarted:
	case <-ctx.Done():
		t.Fatal("upload did not reach the PUT")
	}
	disconnect()
	select {
	case outcome := <-result:
		if outcome != application.NotSent || ctx.Err() != nil {
			t.Fatalf("disconnect did not promptly return known unsent: %s %v", outcome, ctx.Err())
		}
	case <-ctx.Done():
		t.Fatal("upload kept waiting after disconnect")
	}
	select {
	case <-putCanceled:
	case <-ctx.Done():
		t.Fatal("HTTP server did not observe cancellation")
	}
}
