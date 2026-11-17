package application

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type queuedReceiptRepository struct {
	Repository
	MessagingRepository
	account domain.ID
}

func (r queuedReceiptRepository) Conversation(_ context.Context, id domain.ID) (domain.Conversation, error) {
	return domain.Conversation{ID: id, AccountID: r.account}, nil
}

type queuedReceiptProtocol struct {
	release <-chan struct{}
	handled chan<- domain.ID
}

func (p queuedReceiptProtocol) DisplayedMarkerTransport(domain.ID) string { return "transport" }

func (p queuedReceiptProtocol) SendDisplayedMarker(ctx context.Context, _ domain.Conversation, marker domain.DisplayedMarker) {
	select {
	case <-ctx.Done():
		return
	case <-p.release:
		p.handled <- marker.ConversationID
	}
}

func TestDisplayedWorkerKeepsBurstWhileSendIsBlocked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const account domain.ID = "019a5df0-0000-7000-8000-000000000001"
		release := make(chan struct{})
		handled := make(chan domain.ID, 66)
		protocol := queuedReceiptProtocol{release: release, handled: handled}
		engine, err := New(queuedReceiptRepository{account: account}, "local", nil, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = engine.runDisplayedMarkers(ctx, protocol) }()

		queue := func(n int) domain.ID {
			id := domain.ID(fmt.Sprintf("019a5df0-0000-7000-8000-%012d", n))
			engine.queueDisplayedMarker(&domain.DisplayedMarker{ConversationID: id, AccountID: account})
			return id
		}
		expected := []domain.ID{queue(1)}
		synctest.Wait()
		for n := 2; n <= cap(handled); n++ {
			expected = append(expected, queue(n))
		}
		close(release)
		synctest.Wait()

		for _, want := range expected {
			select {
			case got := <-handled:
				if got != want {
					t.Fatalf("handled %s, want %s", got, want)
				}
			default:
				t.Fatalf("conversation %s never reached the protocol", want)
			}
		}
	})
}
