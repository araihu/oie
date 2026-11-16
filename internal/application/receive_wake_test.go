package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type receiveWakeRepository struct {
	Repository
	MessagingRepository
	result error
}

func (r receiveWakeRepository) Account(_ context.Context, id domain.ID) (domain.Account, error) {
	return domain.Account{ID: id, Protocol: "xmpp"}, nil
}

func (r receiveWakeRepository) ReceiveMessage(context.Context, domain.Conversation, domain.Message, domain.ID) error {
	return r.result
}

type receiveWakeProtocols struct{ Protocols }

func (receiveWakeProtocols) NormalizeAddress(_, address string) (string, error) {
	return address, nil
}

func TestReceiveWakesEventConsumersAfterCommit(t *testing.T) {
	const account domain.ID = "019a5df0-0000-7000-8000-000000000001"
	storageErr := errors.New("receive transaction failed")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "committed"},
		{name: "failed", err: storageErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := New(receiveWakeRepository{result: tc.err}, "hosted", func() (domain.ID, error) {
				return account, nil
			}, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			engine.SetProtocols(receiveWakeProtocols{})
			wake := engine.EventWake()
			err = engine.Receive(t.Context(), Incoming{
				AccountID:    account,
				Address:      "sender@example.org",
				Kind:         "direct",
				OriginalText: "New incoming message",
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("Receive: %v, want %v", err, tc.err)
			}
			select {
			case <-wake:
				if tc.err != nil {
					t.Fatal("event consumers woke before a successful commit")
				}
			default:
				if tc.err == nil {
					t.Fatal("event consumers did not wake for a committed message")
				}
			}
		})
	}
}
