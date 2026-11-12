package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func decodeRoomIntent(row dbgen.RoomIntent) (domain.RoomSession, error) {
	at, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	state := "not_joined"
	if row.LastErrorCode != "" {
		state = "failed"
	}
	return domain.RoomSession{AccountID: domain.ID(row.AccountID), ConversationID: domain.ID(row.ConversationID), IntentID: domain.ID(row.ID), IntentRevisionID: domain.ID(row.RevisionID), Active: row.Active == 1, Nickname: row.Nickname, LastErrorCode: row.LastErrorCode, UpdatedAt: at, State: state}, err
}
func (s *Store) RoomIntent(ctx context.Context, id domain.ID) (domain.RoomSession, error) {
	row, err := s.queries.GetRoomIntent(ctx, string(id))
	if err != nil {
		return domain.RoomSession{}, notFound(err)
	}
	return decodeRoomIntent(row)
}
func (s *Store) RoomIntents(ctx context.Context) ([]domain.RoomSession, error) {
	rows, err := s.queries.ListRoomIntents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.RoomSession, 0, len(rows))
	for _, row := range rows {
		s, err := decodeRoomIntent(row)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
func (s *Store) ReplayRoomIntent(ctx context.Context, id domain.ID, fingerprint string) (*domain.RoomSession, error) {
	row, err := s.queries.GetOperation(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Fingerprint != fingerprint || (row.Kind != "join_room" && row.Kind != "leave_room") {
		return nil, application.ErrIdempotencyConflict
	}
	var result domain.RoomSession
	if err = json.Unmarshal([]byte(row.Result), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (s *Store) SetRoomIntent(ctx context.Context, in application.RoomIntentMutation) (application.RoomIntentResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.RoomIntentResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, in.Session)
	if err != nil {
		return application.RoomIntentResult{}, err
	}
	if receipt != nil {
		var result domain.RoomSession
		err = json.Unmarshal([]byte(receipt.Result), &result)
		return application.RoomIntentResult{Session: result, Replayed: true}, err
	}
	row, err := q.GetConversation(ctx, string(in.Session.ConversationID))
	if err != nil {
		return application.RoomIntentResult{}, notFound(err)
	}
	c, err := decodeConversation(row)
	if err != nil {
		return application.RoomIntentResult{}, err
	}
	if c.AccountID != in.Session.AccountID || c.Kind != "room" {
		return application.RoomIntentResult{}, application.ErrInvalid
	}
	old, err := q.GetRoomIntent(ctx, string(c.ID))
	if err == nil {
		current, err := decodeRoomIntent(old)
		if err != nil {
			return application.RoomIntentResult{}, err
		}
		if current.IntentRevisionID != in.ExpectedRevisionID {
			return application.RoomIntentResult{}, application.RoomStateConflict{Current: current}
		}
		in.Session.IntentID = current.IntentID
		if !in.Session.Active {
			in.Session.Nickname = current.Nickname
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return application.RoomIntentResult{}, err
	} else if in.ExpectedRevisionID != "" {
		return application.RoomIntentResult{}, application.RoomStateConflict{Current: domain.RoomSession{AccountID: c.AccountID, ConversationID: c.ID, State: "not_joined", UpdatedAt: in.Operation.CreatedAt}}
	}
	if err = putRoomIntent(ctx, q, in.Session); err != nil {
		return application.RoomIntentResult{}, err
	}
	if !in.Session.Active {
		if err := q.ClearRoomReactionVisibility(ctx, dbgen.ClearRoomReactionVisibilityParams{AccountID: string(c.AccountID), Address: c.Address}); err != nil {
			return application.RoomIntentResult{}, err
		}
	}
	if _, err = change(ctx, q, "conversation.room_session_changed", in.Session, in.Session.UpdatedAt); err != nil {
		return application.RoomIntentResult{}, err
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ResourceID: string(c.ID), Result: jsonText(in.Session), ID: string(in.Operation.ID)}); err != nil {
		return application.RoomIntentResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.RoomIntentResult{}, err
	}
	return application.RoomIntentResult{Session: in.Session}, nil
}
func putRoomIntent(ctx context.Context, q *dbgen.Queries, s domain.RoomSession) error {
	return q.PutRoomIntent(ctx, dbgen.PutRoomIntentParams{ConversationID: string(s.ConversationID), AccountID: string(s.AccountID), ID: string(s.IntentID), RevisionID: string(s.IntentRevisionID), Nickname: s.Nickname, Active: number(s.Active), LastErrorCode: s.LastErrorCode, UpdatedAt: stamp(s.UpdatedAt)})
}
func (s *Store) RevokeRoomIntent(ctx context.Context, id, expected, revision domain.ID, code string, at time.Time) (domain.RoomSession, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.RoomSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := q.GetRoomIntent(ctx, string(id))
	if err != nil {
		return domain.RoomSession{}, notFound(err)
	}
	current, err := decodeRoomIntent(row)
	if err != nil {
		return current, err
	}
	if current.IntentRevisionID != expected || !current.Active {
		return current, application.RoomStateConflict{Current: current}
	}
	current.Active = false
	current.IntentRevisionID = revision
	current.LastErrorCode = code
	current.State = "failed"
	current.UpdatedAt = at
	if err = putRoomIntent(ctx, q, current); err != nil {
		return current, err
	}
	if _, err = change(ctx, q, "conversation.room_session_changed", current, at); err != nil {
		return current, err
	}
	return current, tx.Commit()
}
func (s *Store) RecordRoomSession(ctx context.Context, state domain.RoomSession) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	current, err := q.GetRoomIntent(ctx, string(state.ConversationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.RevisionID != string(state.IntentRevisionID) || current.AccountID != string(state.AccountID) {
		return nil
	}
	data, _ := json.Marshal([]any{state.IntentRevisionID, state.State, state.Nickname, state.LastErrorCode, state.Active})
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if digest == current.ObservationFingerprint {
		return nil
	}
	if err = q.SetRoomObservation(ctx, dbgen.SetRoomObservationParams{ObservationFingerprint: digest, ConversationID: string(state.ConversationID), RevisionID: string(state.IntentRevisionID)}); err != nil {
		return err
	}
	if _, err = change(ctx, q, "conversation.room_session_changed", state, state.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplayAcceptedMessage(ctx context.Context, id domain.ID, fingerprint string) (*domain.Message, error) {
	return s.ReplayMessageMutation(ctx, id, "send_message", fingerprint)
}

func (s *Store) ReplayMessageMutation(ctx context.Context, id domain.ID, kind, fingerprint string) (*domain.Message, error) {
	row, err := s.queries.GetOperation(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Kind != kind || row.Fingerprint != fingerprint {
		return nil, application.ErrIdempotencyConflict
	}
	var result domain.Message
	if err = json.Unmarshal([]byte(row.Result), &result); err != nil {
		return nil, err
	}
	if err = hydrateReply(ctx, s.queries, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ClaimRoomMessage makes the desired-room cutoff and outbox claim one write
// transaction. A concurrent leave either precedes this claim or observes work
// already dispatching; it cannot silently reopen a queued send.
func (s *Store) ClaimRoomMessage(ctx context.Context, id, intentRevision, revision domain.ID, at time.Time) (domain.Message, error) {
	return s.claimDispatchMessage(ctx, id, "", intentRevision, revision, at, true, false)
}
