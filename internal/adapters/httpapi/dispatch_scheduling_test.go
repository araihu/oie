package httpapi_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
	"github.com/araihu/oie/internal/adapters/sqlite"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func TestScheduleOffsetCannotOverflowUTCYear(t *testing.T) {
	ctx := t.Context()
	engine, _, server := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	conversation := openTestConversation(t, engine, "alice@localhost")
	client, err := engineapi.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := time.Parse(time.RFC3339, "9999-12-31T23:59:59-01:00")
	if err != nil {
		t.Fatal(err)
	}
	send, err := client.SendMessageWithResponse(ctx, string(conversation.ID), &engineapi.SendMessageParams{IdempotencyKey: string(id())},
		engineapi.SendMessage{OriginalText: "Test schedule", ScheduledAt: &invalid})
	if err != nil {
		t.Fatal(err)
	}
	if send.StatusCode() != http.StatusBadRequest {
		t.Fatalf("invalid offset accepted: %s", send.Body)
	}

	at := time.Now().Add(time.Hour).UTC()
	scheduled, err := engine.SendComposition(ctx, id(), conversation.ID, domain.OutgoingComposition{OriginalText: "Existing schedule"}, &at)
	if err != nil {
		t.Fatal(err)
	}
	edit, err := client.UpdatePendingMessageWithResponse(ctx, string(scheduled.Message.ID), &engineapi.UpdatePendingMessageParams{IdempotencyKey: string(id())},
		engineapi.UpdatePendingMessage{ExpectedRevisionId: string(scheduled.Message.RevisionID), ScheduledAt: &invalid})
	if err != nil {
		t.Fatal(err)
	}
	if edit.StatusCode() != http.StatusBadRequest {
		t.Fatalf("invalid reschedule offset accepted: %s", edit.Body)
	}
	current, err := engine.Message(ctx, scheduled.Message.ID)
	if err != nil || current.ScheduledAt == nil || !current.ScheduledAt.Equal(at) {
		t.Fatalf("invalid reschedule changed message: %+v %v", current, err)
	}
}

type pausedPendingReplayStore struct {
	*sqlite.Store
	firstRead chan struct{}
	resume    chan struct{}
	read      atomic.Bool
}

func (s *pausedPendingReplayStore) ReplayMessageMutation(ctx context.Context, key domain.ID, kind, hash string) (*domain.Message, error) {
	message, err := s.Store.ReplayMessageMutation(ctx, key, kind, hash)
	if !s.read.Swap(true) {
		close(s.firstRead)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return message, err
}

func TestConcurrentPendingCompositionReplayReturnsSameRevision(t *testing.T) {
	engine, store, _ := newMessagingEngine(t, t.TempDir(), &protocolStub{})
	conversation := openTestConversation(t, engine, "alice@localhost")
	at := time.Now().Add(time.Hour).UTC()
	initial, err := engine.SendComposition(t.Context(), id(), conversation.ID, domain.OutgoingComposition{OriginalText: "Initial"}, &at)
	if err != nil {
		t.Fatal(err)
	}
	paused := &pausedPendingReplayStore{Store: store, firstRead: make(chan struct{}), resume: make(chan struct{})}
	engine, err = application.New(paused, "local", func() (domain.ID, error) { return id(), nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	key := id()
	input := &domain.OutgoingComposition{OriginalText: "Edited"}
	type response struct {
		result application.MessageResult
		err    error
	}
	firstDone := make(chan response, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		result, err := engine.UpdatePendingMessage(ctx, key, initial.Message.ID, initial.Message.RevisionID, input, nil, false)
		firstDone <- response{result, err}
	}()
	select {
	case <-paused.firstRead:
	case <-ctx.Done():
		t.Fatal("first request did not read the original receipt", ctx.Err())
	}
	committed, err := engine.UpdatePendingMessage(ctx, key, initial.Message.ID, initial.Message.RevisionID, input, nil, false)
	close(paused.resume)
	if err != nil || committed.Replayed {
		t.Fatalf("second request did not commit: %+v %v", committed, err)
	}
	select {
	case first := <-firstDone:
		if first.err != nil || !first.result.Replayed || first.result.Message.RevisionID != committed.Message.RevisionID {
			t.Fatalf("first request did not replay the committed revision: %+v %v", first.result, first.err)
		}
	case <-ctx.Done():
		t.Fatal("first request did not finish", ctx.Err())
	}
}
