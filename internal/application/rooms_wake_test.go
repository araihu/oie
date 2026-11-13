package application

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/araihu/oie/internal/domain"
)

type roomWakeRepo struct {
	Repository
	MessagingRepository
	RoomIntentRepository
	account      domain.Account
	conversation domain.Conversation
	intent       domain.RoomSession
	scans        int
}

func (r *roomWakeRepo) Account(context.Context, domain.ID) (domain.Account, error) {
	return r.account, nil
}

func (r *roomWakeRepo) Conversation(context.Context, domain.ID) (domain.Conversation, error) {
	return r.conversation, nil
}

func (r *roomWakeRepo) RoomIntent(context.Context, domain.ID) (domain.RoomSession, error) {
	return r.intent, nil
}

func (r *roomWakeRepo) RoomIntents(context.Context) ([]domain.RoomSession, error) {
	r.scans++
	if r.intent.IntentRevisionID == "" {
		return nil, nil
	}
	return []domain.RoomSession{r.intent}, nil
}

func (r *roomWakeRepo) ReplayRoomIntent(context.Context, domain.ID, string) (*domain.RoomSession, error) {
	return nil, nil
}

func (r *roomWakeRepo) SetRoomIntent(_ context.Context, in RoomIntentMutation) (RoomIntentResult, error) {
	r.intent = in.Session
	return RoomIntentResult{Session: r.intent}, nil
}

func (r *roomWakeRepo) RecordConnection(context.Context, domain.Connection) error { return nil }

type roomWakeProtocol struct {
	Protocols
	mu            sync.Mutex
	connected     bool
	session       domain.RoomSession
	joins, leaves int
}

func (p *roomWakeProtocol) Connection(context.Context, domain.Account) (domain.Connection, error) {
	if p.connected {
		return domain.Connection{State: "connected"}, nil
	}
	return domain.Connection{State: "not_connected"}, nil
}

func (p *roomWakeProtocol) RoomSession(context.Context, domain.Account, domain.Conversation) (domain.RoomSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session, nil
}

func (p *roomWakeProtocol) JoinRoom(_ context.Context, _ domain.Account, _ domain.Conversation, _ string, revision domain.ID) (domain.RoomSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.joins++
	p.session.State, p.session.IntentRevisionID = "joined", revision
	return p.session, nil
}

func (p *roomWakeProtocol) LeaveRoom(_ context.Context, _ domain.Account, _ domain.Conversation, revision domain.ID) (domain.RoomSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leaves++
	p.session.State, p.session.IntentRevisionID = "not_joined", revision
	return p.session, nil
}

type revokingRoomProtocol struct {
	roomWakeProtocol
	repo    *roomWakeRepo
	revoked domain.RoomSession
}

func (p *revokingRoomProtocol) RoomSession(context.Context, domain.Account, domain.Conversation) (domain.RoomSession, error) {
	// The XMPP adapter can finish committing a revocation while the read waits
	// on its notification lock. Its observation still refers to the old join.
	p.repo.intent = p.revoked
	return p.session, nil
}

func TestRoomSessionUsesRevisionCommittedDuringProtocolRead(t *testing.T) {
	const accountID domain.ID = "019a5df0-0000-7000-8000-000000000001"
	const roomID domain.ID = "019a5df0-0000-7000-8000-000000000002"
	const joinRevision domain.ID = "019a5df0-0000-7000-8000-000000000003"
	const failedRevision domain.ID = "019a5df0-0000-7000-8000-000000000004"
	intent := domain.RoomSession{AccountID: accountID, ConversationID: roomID, IntentRevisionID: joinRevision, Active: true}
	repo := &roomWakeRepo{account: domain.Account{ID: accountID}, conversation: domain.Conversation{ID: roomID, AccountID: accountID, Kind: "room"}, intent: intent}
	protocol := &revokingRoomProtocol{repo: repo, revoked: intent}
	protocol.session = domain.RoomSession{AccountID: accountID, ConversationID: roomID, IntentRevisionID: joinRevision, State: "failed", LastErrorCode: "nickname_conflict", Revoked: true}
	protocol.revoked.IntentRevisionID = failedRevision
	protocol.revoked.Active = false
	protocol.revoked.LastErrorCode = "nickname_conflict"
	e, err := New(repo, "local", func() (domain.ID, error) { return roomID, nil }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	e.SetProtocols(protocol)
	session, err := e.RoomSession(t.Context(), roomID)
	if err != nil || session.State != "failed" || session.LastErrorCode != "nickname_conflict" || session.Active || session.IntentRevisionID != failedRevision {
		t.Fatal("failed join must expose the committed revision for retry", session, err)
	}
}

func TestRoomIntentAndReconnectWakeWorkerWithoutTicker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const accountID domain.ID = "019a5df0-0000-7000-8000-000000000001"
		const roomID domain.ID = "019a5df0-0000-7000-8000-000000000002"
		const joinKey domain.ID = "019a5df0-0000-7000-8000-000000000003"
		const leaveKey domain.ID = "019a5df0-0000-7000-8000-000000000004"
		repo := &roomWakeRepo{account: domain.Account{ID: accountID}, conversation: domain.Conversation{ID: roomID, AccountID: accountID, Kind: "room"}}
		protocol := &roomWakeProtocol{session: domain.RoomSession{AccountID: accountID, ConversationID: roomID, State: "not_joined"}}
		e, err := New(repo, "local", func() (domain.ID, error) { return roomID, nil }, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		e.SetProtocols(protocol)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- e.runRooms(ctx) }()
		synctest.Wait()
		started := time.Now()
		wake := e.EventWake()
		joined, err := e.JoinRoom(ctx, joinKey, roomID, "Alice", "")
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if repo.scans != 2 || protocol.joins != 0 {
			t.Fatal("committed intent should wake the worker even while disconnected", repo.scans, protocol.joins)
		}
		select {
		case <-wake:
		default:
			t.Fatal("committed intent did not notify event readers")
		}
		protocol.connected = true
		if err := e.ConnectionChanged(ctx, domain.Connection{AccountID: accountID, State: "connected"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if protocol.joins != 1 {
			t.Fatal("room did not join on reconnect", protocol.joins)
		}
		if _, err := e.LeaveRoom(ctx, leaveKey, roomID, joined.Session.IntentRevisionID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if protocol.leaves != 1 || !time.Now().Equal(started) {
			t.Fatal("room operations waited for the ticker", protocol.leaves, time.Since(started))
		}
		cancel()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}
