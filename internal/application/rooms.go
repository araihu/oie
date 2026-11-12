package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/araihu/oie/internal/domain"
)

var ErrRoomNotJoined = errors.New("room entry has not been requested")

type RoomStateConflict struct{ Current domain.RoomSession }

func (e RoomStateConflict) Error() string { return ErrStateConflict.Error() }
func (e RoomStateConflict) Unwrap() error { return ErrStateConflict }

type RoomProtocol interface {
	JoinRoom(context.Context, domain.Account, domain.Conversation, string, domain.ID) (domain.RoomSession, error)
	LeaveRoom(context.Context, domain.Account, domain.Conversation, domain.ID) (domain.RoomSession, error)
	RoomSession(context.Context, domain.Account, domain.Conversation) (domain.RoomSession, error)
}
type RoomProtocolHost interface {
	RoomSessionChanged(context.Context, domain.RoomSession) error
}
type RoomIntentMutation struct {
	Operation          Operation
	Fingerprint        string
	Session            domain.RoomSession
	ExpectedRevisionID domain.ID
}
type RoomIntentResult struct {
	Session  domain.RoomSession
	Replayed bool
}
type RoomMessageClaimer interface {
	ClaimRoomMessage(context.Context, domain.ID, domain.ID, domain.ID, time.Time) (domain.Message, error)
}

type RoomIntentRepository interface {
	RoomIntent(context.Context, domain.ID) (domain.RoomSession, error)
	RoomIntents(context.Context) ([]domain.RoomSession, error)
	SetRoomIntent(context.Context, RoomIntentMutation) (RoomIntentResult, error)
	RevokeRoomIntent(context.Context, domain.ID, domain.ID, domain.ID, string, time.Time) (domain.RoomSession, error)
	ReplayRoomIntent(context.Context, domain.ID, string) (*domain.RoomSession, error)
	RecordRoomSession(context.Context, domain.RoomSession) error
}

func (e *Engine) roomIntent(ctx context.Context, id domain.ID) (domain.RoomSession, error) {
	r, ok := e.repo.(RoomIntentRepository)
	if !ok {
		return domain.RoomSession{}, ErrProtocolUnavailable
	}
	s, err := r.RoomIntent(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return domain.RoomSession{ConversationID: id, State: "not_joined"}, nil
	}
	return s, err
}
func (e *Engine) RoomSession(ctx context.Context, id domain.ID) (domain.RoomSession, error) {
	c, err := e.Conversation(ctx, id)
	if err != nil {
		return domain.RoomSession{}, err
	}
	if c.Kind != "room" {
		return domain.RoomSession{}, ErrInvalid
	}
	result, err := e.roomIntent(ctx, id)
	if err != nil {
		return result, err
	}
	result.AccountID = c.AccountID
	result.ConversationID = c.ID
	if result.State == "" {
		result.State = "not_joined"
	}
	if result.UpdatedAt.IsZero() {
		result.UpdatedAt = c.UpdatedAt
	}
	a, err := e.Account(ctx, c.AccountID)
	if err != nil {
		return result, err
	}
	provider, ok := e.protocols.(RoomProtocol)
	if !ok {
		result.State = "failed"
		result.LastErrorCode = "unsupported"
		return result, nil
	}
	observed, err := provider.RoomSession(ctx, a, c)
	if err != nil {
		return result, err
	}
	if observed.AccountID != a.ID || observed.ConversationID != c.ID {
		return result, ErrProtocolUnavailable
	}
	if observed.LastErrorCode == "not_connected" {
		if result.LastErrorCode == "" || result.LastErrorCode == "not_connected" {
			result.State = "not_joined"
			result.LastErrorCode = "not_connected"
		} else {
			result.State = "failed"
		}
		return result, nil
	}
	if observed.IntentRevisionID != result.IntentRevisionID {
		result.State = "not_joined"
		if result.Active {
			result.State = "joining"
		} else if result.LastErrorCode != "" {
			result.State = "failed"
		}
		return result, nil
	}
	if observed.Nickname != "" {
		result.Nickname = observed.Nickname
	}
	result.State = observed.State
	if result.State == "" {
		result.State = "not_joined"
	}
	if observed.LastErrorCode != "" {
		result.LastErrorCode = observed.LastErrorCode
	}
	if !observed.UpdatedAt.IsZero() {
		result.UpdatedAt = observed.UpdatedAt
	}
	return result, nil
}
func (e *Engine) JoinRoom(ctx context.Context, key, id domain.ID, nickname string, expected domain.ID) (RoomIntentResult, error) {
	return e.setRoomIntent(ctx, key, id, nickname, expected, true)
}
func (e *Engine) LeaveRoom(ctx context.Context, key, id, expected domain.ID) (RoomIntentResult, error) {
	return e.setRoomIntent(ctx, key, id, "", expected, false)
}
func (e *Engine) setRoomIntent(ctx context.Context, key, id domain.ID, nickname string, expected domain.ID, active bool) (RoomIntentResult, error) {
	if !validID(key) || !validID(id) || (expected != "" && !validID(expected)) {
		return RoomIntentResult{}, ErrInvalid
	}
	nickname = strings.TrimSpace(nickname)
	if active && !validRoomNickname(nickname) {
		return RoomIntentResult{}, ErrInvalid
	}
	repo, ok := e.repo.(RoomIntentRepository)
	if !ok {
		return RoomIntentResult{}, ErrProtocolUnavailable
	}
	kind := "leave_room"
	if active {
		kind = "join_room"
	}
	fp := fingerprint(kind, []string{string(id), nickname, string(expected)})
	replay, err := repo.ReplayRoomIntent(ctx, key, fp)
	if err != nil {
		return RoomIntentResult{}, err
	}
	if replay != nil {
		s, err := e.RoomSession(ctx, id)
		return RoomIntentResult{Session: s, Replayed: true}, err
	}
	c, err := e.Conversation(ctx, id)
	if err != nil {
		return RoomIntentResult{}, err
	}
	if c.Kind != "room" {
		return RoomIntentResult{}, ErrInvalid
	}
	if _, ok := e.protocols.(RoomProtocol); !ok {
		return RoomIntentResult{}, ErrProtocolUnavailable
	}
	intentID, err := e.newID()
	if err != nil {
		return RoomIntentResult{}, err
	}
	rev, err := e.newID()
	if err != nil {
		return RoomIntentResult{}, err
	}
	now := e.now().UTC()
	result, err := repo.SetRoomIntent(ctx, RoomIntentMutation{Operation: Operation{ID: key, ResourceID: id, Kind: kind, CreatedAt: now}, Fingerprint: fp, Session: domain.RoomSession{AccountID: c.AccountID, ConversationID: id, IntentID: intentID, IntentRevisionID: rev, Active: active, Nickname: nickname, State: "not_joined", UpdatedAt: now}, ExpectedRevisionID: expected})
	if err != nil {
		var conflict RoomStateConflict
		if errors.As(err, &conflict) {
			if current, readErr := e.RoomSession(ctx, id); readErr == nil {
				conflict.Current = current
			}
			return result, conflict
		}
		return result, err
	}
	e.wakeRooms()
	e.outgoingWorkCommitted()
	result.Session, err = e.RoomSession(ctx, id)
	return result, err
}
func (e *Engine) RoomSessionChanged(ctx context.Context, s domain.RoomSession) error {
	c, err := e.Conversation(ctx, s.ConversationID)
	if err != nil {
		return err
	}
	if c.Kind != "room" || c.AccountID != s.AccountID {
		return ErrInvalid
	}
	if s.State != "not_joined" && s.State != "joining" && s.State != "joined" && s.State != "failed" {
		return ErrInvalid
	}
	intent, err := e.roomIntent(ctx, c.ID)
	if err != nil {
		return err
	}
	if s.IntentRevisionID == "" || s.IntentRevisionID != intent.IntentRevisionID {
		return nil
	}
	current := intent
	if s.Revoked || s.LastErrorCode == "forbidden" || s.LastErrorCode == "not_authorized" || s.LastErrorCode == "registration_required" {
		r := e.repo.(RoomIntentRepository)
		if intent.Active {
			rev, err := e.newID()
			if err != nil {
				return err
			}
			current, err = r.RevokeRoomIntent(ctx, c.ID, intent.IntentRevisionID, rev, s.LastErrorCode, e.now().UTC())
			if errors.Is(err, ErrStateConflict) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	s.IntentID = current.IntentID
	s.IntentRevisionID = current.IntentRevisionID
	s.Active = current.Active
	if s.Nickname == "" {
		s.Nickname = current.Nickname
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = e.now().UTC()
	}
	s.Revoked = false
	if r, ok := e.repo.(RoomIntentRepository); ok {
		if err := r.RecordRoomSession(ctx, s); err != nil {
			return err
		}
	}
	e.outgoingWorkCommitted()
	return nil
}

func (e *Engine) wakeRooms() {
	select {
	case e.roomWork <- struct{}{}:
	default:
	}
}

func (e *Engine) runRooms(ctx context.Context) error {
	r := e.repo.(RoomIntentRepository)
	p := e.protocols.(RoomProtocol)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	next := map[domain.ID]time.Time{}
	versions := map[domain.ID]domain.ID{}
	for {
		intents, err := r.RoomIntents(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, intent := range intents {
			c, err := e.Conversation(ctx, intent.ConversationID)
			if err != nil {
				return err
			}
			a, err := e.Account(ctx, intent.AccountID)
			if err != nil {
				return err
			}
			connection, err := e.Connection(ctx, a.ID)
			if err != nil || connection.State != "connected" {
				continue
			}
			s, err := p.RoomSession(ctx, a, c)
			if err != nil {
				continue
			}
			if versions[c.ID] != intent.IntentRevisionID {
				delete(next, c.ID)
				versions[c.ID] = intent.IntentRevisionID
			}
			if !intent.Active {
				if s.State == "joining" || s.State == "joined" {
					call, cancel := context.WithTimeout(ctx, 10*time.Second)
					current, readErr := e.roomIntent(ctx, c.ID)
					if readErr == nil && !current.Active && current.IntentRevisionID == intent.IntentRevisionID {
						_, _ = p.LeaveRoom(call, a, c, intent.IntentRevisionID)
					}
					cancel()
				}
				continue
			}
			if intent.CreationName != nil && s.IntentRevisionID == intent.IntentRevisionID && s.State == "joined" {
				if err := e.RoomSessionChanged(ctx, s); err != nil && ctx.Err() == nil {
					return err
				}
			}

			if s.IntentRevisionID == intent.IntentRevisionID && (s.State == "joined" || s.State == "joining" || s.LastErrorCode == "nickname_conflict" || s.LastErrorCode == "unsupported") {
				continue
			}
			if e.now().Before(next[c.ID]) {
				continue
			}
			// Re-read intent before directed presence; leaving never enables dispatch.
			current, err := e.roomIntent(ctx, c.ID)
			if err != nil {
				return err
			}
			if !current.Active || current.IntentRevisionID != intent.IntentRevisionID {
				continue
			}
			next[c.ID] = e.now().Add(10 * time.Second)
			call, cancel := context.WithTimeout(ctx, 10*time.Second)
			if intent.CreationName != nil {
				if creator, ok := p.(RoomCreator); ok {
					_, _ = creator.CreateRoom(call, a, c, intent.Nickname, *intent.CreationName, intent.IntentRevisionID)
				}
			} else {
				_, _ = p.JoinRoom(call, a, c, intent.Nickname, intent.IntentRevisionID)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-e.roomWork:
		case <-ticker.C:
		}
	}
}
