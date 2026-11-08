package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func decodeConversation(row dbgen.Conversation) (domain.Conversation, error) {
	var c domain.Conversation
	err := json.Unmarshal([]byte(row.Payload), &c)
	c.ActivitySequence = row.ActivitySequence
	return c, err
}
func decodeMessage(row dbgen.Message) (domain.Message, error) {
	var m domain.Message
	err := json.Unmarshal([]byte(row.Payload), &m)
	domain.ProjectOutboundReply(&m)
	return m, err
}
func number(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
func insertConversation(ctx context.Context, q *dbgen.Queries, c domain.Conversation) (domain.Conversation, error) {
	seq, err := change(ctx, q, "conversation.created", c, c.UpdatedAt)
	if err != nil {
		return c, err
	}
	c.ActivitySequence = seq
	err = q.InsertConversation(ctx, dbgen.InsertConversationParams{ID: string(c.ID), RevisionID: string(c.RevisionID), AccountID: string(c.AccountID), Address: c.Address, PeerID: string(c.PeerID), Payload: jsonText(c), ActivitySequence: seq})
	if err != nil {
		return c, err
	}
	err = q.InsertConversationRevision(ctx, dbgen.InsertConversationRevisionParams{ID: string(c.RevisionID), EntityID: string(c.ID), Parents: "[]", Payload: jsonText(c), CreatedAt: stamp(c.UpdatedAt)})
	return c, err
}
func (s *Store) OpenConversation(ctx context.Context, in application.ConversationMutation) (application.ConversationResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.ConversationResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, in.Conversation)
	if err != nil {
		return application.ConversationResult{}, err
	}
	if receipt != nil {
		var c domain.Conversation
		err = json.Unmarshal([]byte(receipt.Result), &c)
		return application.ConversationResult{Conversation: c, Replayed: true}, err
	}
	row, err := q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(in.Conversation.AccountID), Address: in.Conversation.Address})
	existing := err == nil
	var c domain.Conversation
	if existing {
		c, err = decodeConversation(row)
	} else if errors.Is(err, sql.ErrNoRows) {
		c, err = insertConversation(ctx, q, in.Conversation)
	}
	if err != nil {
		return application.ConversationResult{}, err
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ResourceID: string(c.ID), Result: jsonText(c), ID: string(in.Operation.ID)}); err != nil {
		return application.ConversationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.ConversationResult{}, err
	}
	return application.ConversationResult{Conversation: c, Existing: existing}, nil
}
func (s *Store) Conversation(ctx context.Context, id domain.ID) (domain.Conversation, error) {
	row, err := s.queries.GetConversation(ctx, string(id))
	if err != nil {
		return domain.Conversation{}, notFound(err)
	}
	return decodeConversation(row)
}
func (s *Store) Conversations(ctx context.Context, in application.ConversationQuery) ([]domain.Conversation, error) {
	rows, err := s.queries.ListConversations(ctx, dbgen.ListConversationsParams{Cursor: in.Cursor, AccountID: string(in.AccountID), Archived: number(in.Archived), FavoritesOnly: number(in.FavoritesOnly), UnreadOnly: number(in.UnreadOnly), Kind: in.Kind, Query: in.Query, PageLimit: int64(in.Limit)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Conversation, 0, len(rows))
	for _, row := range rows {
		c, err := decodeConversation(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
func insertMessage(ctx context.Context, q *dbgen.Queries, m domain.Message) error {
	if err := q.InsertMessage(ctx, dbgen.InsertMessageParams{ID: string(m.ID), RevisionID: string(m.RevisionID), AccountID: string(m.AccountID), ConversationID: string(m.ConversationID), ProviderID: m.ProviderID, IsOwn: number(m.IsOwn), State: m.SendState, Payload: jsonText(m), CreatedAtMs: m.CreatedAt.UnixMilli()}); err != nil {
		return err
	}
	if err := q.InsertMessageRevision(ctx, dbgen.InsertMessageRevisionParams{ID: string(m.RevisionID), EntityID: string(m.ID), Parents: "[]", Payload: jsonText(m), CreatedAt: stamp(m.CreatedAt)}); err != nil {
		return err
	}
	if err := recordReplyMetadata(ctx, q, string(m.ID), m, false); err != nil {
		return err
	}
	if err := recordCorrectionMetadata(ctx, q, string(m.ID), m); err != nil {
		return err
	}
	kind := "message.created"
	if correctionValid(m.Correction) && m.Correction.TargetID != "" {
		kind = "message.updated"
	}
	_, err := emitCanonicalMessageEvent(ctx, q, kind, m, m.CreatedAt)
	return err
}
func updateConversation(ctx context.Context, q *dbgen.Queries, c domain.Conversation, m domain.Message, revision domain.ID, at time.Time) error {
	parents := jsonText([]domain.ID{c.RevisionID})
	c.RevisionID = revision
	if m.Historical && c.LastMessage != "" && m.CreatedAt.Before(c.UpdatedAt) {
		return nil
	}
	if c.LastMessage == "" || !m.CreatedAt.Before(c.UpdatedAt) {
		c.LastMessage = m.OriginalText
		c.UpdatedAt = m.CreatedAt
	}
	if !m.IsOwn && !m.Historical {
		c.UnreadCount++
	}
	seq, err := change(ctx, q, "conversation.updated", c, at)
	if err != nil {
		return err
	}
	c.ActivitySequence = seq
	if err = q.UpdateConversation(ctx, dbgen.UpdateConversationParams{RevisionID: string(revision), Payload: jsonText(c), ActivitySequence: seq, UnreadCount: int64(c.UnreadCount), ID: string(c.ID)}); err != nil {
		return err
	}
	return q.InsertConversationRevision(ctx, dbgen.InsertConversationRevisionParams{ID: string(revision), EntityID: string(c.ID), Parents: parents, Payload: jsonText(c), CreatedAt: stamp(at)})
}
func (s *Store) AcceptMessage(ctx context.Context, in application.MessageMutation) (application.MessageResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.MessageResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, in.Message)
	if err != nil {
		return application.MessageResult{}, err
	}
	if receipt != nil {
		var m domain.Message
		err = json.Unmarshal([]byte(receipt.Result), &m)
		if err == nil {
			err = hydrateReply(ctx, q, &m)
		}
		return application.MessageResult{Message: m, Replayed: true}, err
	}
	row, err := q.GetConversation(ctx, string(in.Message.ConversationID))
	if err != nil {
		return application.MessageResult{}, notFound(err)
	}
	c, err := decodeConversation(row)
	if err != nil {
		return application.MessageResult{}, err
	}
	if c.Kind == "room" {
		intent, err := q.GetRoomIntent(ctx, string(c.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return application.MessageResult{}, application.ErrRoomNotJoined
		}
		if err != nil {
			return application.MessageResult{}, err
		}
		if intent.Active != 1 {
			return application.MessageResult{}, application.ErrRoomNotJoined
		}
	}
	if plan := in.Message.OutboundReply; plan != nil {
		target, err := q.GetMessage(ctx, string(plan.TargetID))
		if err != nil {
			return application.MessageResult{}, notFound(err)
		}
		source, err := decodeMessage(target)
		if err != nil {
			return application.MessageResult{}, err
		}
		if err = hydrateMessageProjection(ctx, q, &source); err != nil {
			return application.MessageResult{}, err
		}
		if source.AccountID != in.Message.AccountID || source.ConversationID != in.Message.ConversationID || bodyHash(source.OriginalText) != plan.TargetBodyHash {
			return application.MessageResult{}, application.ErrStateConflict
		}
		if plan.Reference != nil {
			if err = hydrateReferences(ctx, q, &source); err != nil {
				return application.MessageResult{}, err
			}
			found := false
			for _, ref := range source.References {
				if ref == *plan.Reference {
					found = true
					break
				}
			}
			if !found {
				plan.Reference = nil
				in.Message.ReplyReference = nil
			}
		}

	}
	if err = insertMessage(ctx, q, in.Message); err != nil {
		return application.MessageResult{}, err
	}
	if err = q.InsertOutbox(ctx, dbgen.InsertOutboxParams{MessageID: string(in.Message.ID), NextAttemptAt: stamp(in.Message.CreatedAt)}); err != nil {
		return application.MessageResult{}, err
	}
	if err = updateConversation(ctx, q, c, in.Message, in.ConversationRevisionID, in.Message.CreatedAt); err != nil {
		return application.MessageResult{}, err
	}
	if err = hydrateReply(ctx, q, &in.Message); err != nil {
		return application.MessageResult{}, err
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ResourceID: string(in.Message.ID), Result: jsonText(in.Message), ID: string(in.Operation.ID)}); err != nil {
		return application.MessageResult{}, err
	}

	if err = tx.Commit(); err != nil {
		return application.MessageResult{}, err
	}
	in.Message.References = nil
	in.Message.ReplyReference = nil
	return application.MessageResult{Message: in.Message}, nil
}
func (s *Store) Message(ctx context.Context, id domain.ID) (domain.Message, error) {
	canonical, err := resolveMessageAlias(ctx, s.queries, id)
	if err != nil {
		return domain.Message{}, err
	}
	row, err := s.queries.GetMessage(ctx, string(canonical))
	if err != nil {
		return domain.Message{}, notFound(err)
	}
	m, err := decodeMessage(row)
	if err == nil {
		err = hydrateMessageProjection(ctx, s.queries, &m)
		if err == nil {
			err = hydrateReply(ctx, s.queries, &m)
		}
		if err == nil {
			err = hydrateReferences(ctx, s.queries, &m)
		}
	}
	return m, err
}
func (s *Store) Messages(ctx context.Context, id, cursor domain.ID, limit int) ([]domain.Message, error) {
	canonical, err := resolveMessageAlias(ctx, s.queries, cursor)
	if err != nil {
		return nil, err
	}
	cursor = canonical
	rows, err := s.queries.ListMessages(ctx, dbgen.ListMessagesParams{PageConversationID: string(id), PageCursor: string(cursor), PageLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	items, err := decodeMessages(rows)
	if err != nil {
		return nil, err
	}
	return s.hydrateReplies(ctx, items)
}
func decodeMessages(rows []dbgen.Message) ([]domain.Message, error) {
	out := make([]domain.Message, 0, len(rows))
	for _, row := range rows {
		m, err := decodeMessage(row)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
func (s *Store) ReceiveMessage(ctx context.Context, c domain.Conversation, m domain.Message, revision domain.ID) error {
	at := c.UpdatedAt
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	if m.ArchiveID != "" {
		var existingID string
		existingID, err = q.GetArchiveAlias(ctx, dbgen.GetArchiveAliasParams{AccountID: string(m.AccountID), Scope: m.ArchiveScope, ArchiveID: m.ArchiveID})
		if err == nil {
			retained, err := q.GetMessage(ctx, existingID)
			if err != nil {
				return err
			}
			chat, err := q.GetConversation(ctx, retained.ConversationID)
			if err != nil {
				return err
			}
			if chat.AccountID != string(c.AccountID) || chat.Address != c.Address {
				return application.ErrInvalid
			}
			if err = recordCorrectionMetadata(ctx, q, existingID, m); err != nil {
				return err
			}
			if err = s.reconcileArchivedAuthor(ctx, q, existingID, c, m, revision); err != nil {
				return err
			}
			if err = recordReplyMetadata(ctx, q, existingID, m, true); err != nil {
				return err
			}
			return tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	row, err := q.FindConversation(ctx, dbgen.FindConversationParams{AccountID: string(c.AccountID), Address: c.Address})
	if err == nil {
		c, err = decodeConversation(row)
	} else if errors.Is(err, sql.ErrNoRows) {
		c, err = insertConversation(ctx, q, c)
	}
	if err != nil {
		return err
	}
	m.ConversationID = c.ID
	if !m.IsOwn {
		m.Sender.ID = c.PeerID
	}
	if m.ProviderID != "" {
		var existing dbgen.Message
		existing, err = q.FindProviderMessage(ctx, dbgen.FindProviderMessageParams{AccountID: string(m.AccountID), ConversationID: string(c.ID), ProviderID: m.ProviderID, IsOwn: number(m.IsOwn)})
		if err == nil {
			if err = archiveAlias(ctx, q, m, existing.ID); err != nil {
				return err
			}
			if err = recordCorrectionMetadata(ctx, q, existing.ID, m); err != nil {
				return err
			}
			if err = recordReplyMetadata(ctx, q, existing.ID, m, true); err != nil {
				return err
			}
			return tx.Commit()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if err = insertMessage(ctx, q, m); err != nil {
		return err
	}
	if err = archiveAlias(ctx, q, m, string(m.ID)); err != nil {
		return err
	}
	if !correctionValid(m.Correction) || m.Correction.TargetID == "" {
		fresh, err := q.GetConversation(ctx, string(c.ID))
		if err != nil {
			return err
		}
		c, err = decodeConversation(fresh)
		if err != nil {
			return err
		}
		projected := m
		if err = hydrateMessageProjection(ctx, q, &projected); err != nil {
			return err
		}
		if projected.ID == m.ID {
			if err = updateConversation(ctx, q, c, projected, revision, at); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func updateMessage(ctx context.Context, q *dbgen.Queries, m domain.Message, state string, revision domain.ID, at, next time.Time) (domain.Message, error) {
	parents := jsonText([]domain.ID{m.RevisionID})
	m.RevisionID = revision
	m.SendState = state
	if err := q.UpdateMessage(ctx, dbgen.UpdateMessageParams{RevisionID: string(revision), State: state, Payload: jsonText(m), ID: string(m.ID)}); err != nil {
		return m, err
	}
	if err := q.InsertMessageRevision(ctx, dbgen.InsertMessageRevisionParams{ID: string(revision), EntityID: string(m.ID), Parents: parents, Payload: jsonText(m), CreatedAt: stamp(at)}); err != nil {
		return m, err
	}
	increment := int64(0)
	if state == "dispatching" {
		increment = 1
	}
	if err := q.UpdateOutbox(ctx, dbgen.UpdateOutboxParams{State: state, NextAttemptAt: stamp(next), AttemptIncrement: increment, MessageID: string(m.ID)}); err != nil {
		return m, err
	}
	if err := hydrateReply(ctx, q, &m); err != nil {
		return m, err
	}
	_, err := emitCanonicalMessageEvent(ctx, q, "message.updated", m, at)
	return m, err
}
func (s *Store) CancelMessage(ctx context.Context, in application.CancelMutation) (application.MessageResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.MessageResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	receipt, err := operation(ctx, q, in.Operation, in.Fingerprint, struct{}{})
	if err != nil {
		return application.MessageResult{}, err
	}
	if receipt != nil {
		var m domain.Message
		err = json.Unmarshal([]byte(receipt.Result), &m)
		if err == nil {
			err = hydrateReply(ctx, q, &m)
		}
		return application.MessageResult{Message: m, Replayed: true}, err
	}
	row, err := q.GetMessage(ctx, string(in.MessageID))
	if err != nil {
		return application.MessageResult{}, notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return application.MessageResult{}, err
	}
	if !m.IsOwn || m.SendState != "queued" || m.RevisionID != in.ExpectedRevisionID {
		return application.MessageResult{}, application.StateConflict{Current: m}
	}
	m, err = updateMessage(ctx, q, m, "cancelled", in.RevisionID, in.Operation.CreatedAt, in.Operation.CreatedAt)
	if err != nil {
		return application.MessageResult{}, err
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ResourceID: string(m.ID), Result: jsonText(m), ID: string(in.Operation.ID)}); err != nil {
		return application.MessageResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.MessageResult{}, err
	}
	return application.MessageResult{Message: m}, nil
}
func (s *Store) PendingMessages(ctx context.Context, at time.Time) ([]domain.Message, error) {
	rows, err := s.queries.PendingMessages(ctx, stamp(at))
	if err != nil {
		return nil, err
	}
	return decodeMessages(rows)
}
func (s *Store) InterruptedMessages(ctx context.Context) ([]domain.Message, error) {
	rows, err := s.queries.InterruptedMessages(ctx)
	if err != nil {
		return nil, err
	}
	return decodeMessages(rows)
}
func (s *Store) TransitionMessage(ctx context.Context, id domain.ID, expected, state string, revision domain.ID, at, next time.Time) (domain.Message, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.Message{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return domain.Message{}, err
	}
	row, err := q.GetMessage(ctx, string(id))
	if err != nil {
		return domain.Message{}, notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return m, err
	}
	if !m.IsOwn || m.SendState != expected {
		return m, application.StateConflict{Current: m}
	}
	m, err = updateMessage(ctx, q, m, state, revision, at, next)
	if err != nil {
		return m, err
	}
	return m, tx.Commit()
}
func (s *Store) RecordConnection(ctx context.Context, c domain.Connection) error {
	_, err := change(ctx, s.queries, "account.connection_changed", c, c.UpdatedAt)
	return err
}

// Replaying verified room identity metadata updates the retained projection and
// revision only. It never creates outbox work or replaces historical content.
func (s *Store) reconcileArchivedAuthor(ctx context.Context, q *dbgen.Queries, id string, c domain.Conversation, in domain.Message, revision domain.ID) error {
	if !in.Historical || !in.IsOwn || c.Kind != "room" {
		return nil
	}
	row, err := q.GetMessage(ctx, id)
	if err != nil {
		return err
	}
	old, err := decodeMessage(row)
	if err != nil {
		return err
	}
	if old.IsOwn || !old.Historical || old.AccountID != in.AccountID {
		return nil
	}
	parents := jsonText([]domain.ID{old.RevisionID})
	old.IsOwn = true
	old.Sender.ID = in.AccountID
	old.SendState = "submitted"
	old.RevisionID = revision
	at := time.Now().UTC()
	if err = q.ReconcileMessageAuthor(ctx, dbgen.ReconcileMessageAuthorParams{RevisionID: string(revision), Payload: jsonText(old), ID: id}); err != nil {
		return err
	}
	if err = q.InsertMessageRevision(ctx, dbgen.InsertMessageRevisionParams{ID: string(revision), EntityID: id, Parents: parents, Payload: jsonText(old), CreatedAt: stamp(at)}); err != nil {
		return err
	}
	_, err = emitCanonicalMessageEvent(ctx, q, "message.updated", old, at)
	return err
}
