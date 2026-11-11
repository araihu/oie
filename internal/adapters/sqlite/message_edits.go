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

func readEditCandidate(ctx context.Context, q *dbgen.Queries, id domain.ID, conversation bool, ignore domain.ID) (application.MessageEditCandidate, error) {
	var out application.MessageEditCandidate
	var row dbgen.Message
	var err error
	if conversation {
		if _, err = q.GetConversation(ctx, string(id)); err != nil {
			return out, notFound(err)
		}
		row, err = q.LatestOwnMessage(ctx, string(id))
	} else {
		canonical, resolveErr := resolveMessageAlias(ctx, q, id)
		if resolveErr != nil {
			return out, resolveErr
		}
		row, err = q.GetMessage(ctx, string(canonical))
	}
	if err != nil {
		return out, notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return out, err
	}
	out.Source = m
	t := domain.MessageEditTarget{MessageID: m.ID, AccountID: m.AccountID, ConversationID: m.ConversationID, OriginalText: m.OriginalText}
	deny := func(reason string) (application.MessageEditCandidate, error) {
		t.Eligible = false
		t.Reason = reason
		out.Target = t
		return out, nil
	}
	history, err := readMessageEditHistory(ctx, q, m.ID)
	if err != nil {
		return out, err
	}
	t.CurrentRevisionID = history.CurrentRevisionID
	var currentSource domain.ID
	for _, item := range history.Items {
		if item.IsCurrent {
			t.OriginalText = item.OriginalText
			currentSource = item.SourceMessageID
		}
	}
	if !m.IsOwn {
		return deny("not_own")
	}
	if m.SendState != "submitted" {
		return deny("not_submitted")
	}
	receipt, err := q.OriginalSendReceipt(ctx, string(m.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return deny("foreign_source")
	}
	if err != nil {
		return out, err
	}
	var original domain.Message
	if json.Unmarshal([]byte(receipt.Result), &original) != nil || original.ID != m.ID || original.AccountID != m.AccountID || original.ConversationID != m.ConversationID || original.ProviderID != string(m.ID) || m.ProviderID != string(m.ID) || m.Sender.ID != m.AccountID {
		return deny("foreign_source")
	}
	latest, err := q.LatestOwnMessage(ctx, string(m.ConversationID))
	if errors.Is(err, sql.ErrNoRows) {
		return deny("not_submitted")
	}
	if err != nil {
		return out, err
	}
	if latest.ID != string(m.ID) {
		return deny("not_latest")
	}
	if currentSource != m.ID {
		sourceRow, err := q.GetMessage(ctx, string(currentSource))
		if err != nil {
			return out, err
		}
		commandRow, err := q.GetMessageEditCommand(ctx, sourceRow.ProviderID)
		if errors.Is(err, sql.ErrNoRows) {
			return deny("foreign_source")
		}
		if err != nil {
			return out, err
		}
		p, err := decodeEdit(commandRow)
		if err != nil {
			return out, err
		}
		if p.Command.MessageID != m.ID || p.Command.AccountID != m.AccountID || p.Command.ConversationID != m.ConversationID || p.Command.OriginalText != t.OriginalText {
			return deny("foreign_source")
		}
	}
	if len(history.Items) >= 128 {
		return deny("history_unavailable")
	}
	crow, err := q.GetConversation(ctx, string(m.ConversationID))
	if err != nil {
		return out, err
	}
	c, err := decodeConversation(crow)
	if err != nil {
		return out, err
	}
	if (c.Kind != "direct" && c.Kind != "room") || original.OutboundReply != nil || len(original.Mentions) > 0 || len(m.Mentions) > 0 || m.OutboundReply != nil || len(m.MediaThumbnails) > 0 || len(m.Attachments) > 0 {
		return deny("unsupported_payload")
	}
	if active, err := q.GetActiveMessageEdit(ctx, string(m.ID)); err == nil && active.ID != string(ignore) {
		return deny("edit_pending")
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	t.Eligible = true
	out.Target = t
	return out, nil
}

func (s *Store) ReadMessageEditTarget(ctx context.Context, id domain.ID, conversation bool) (application.MessageEditCandidate, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.MessageEditCandidate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	out, err := readEditCandidate(ctx, q, id, conversation, "")
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) MessageSource(ctx context.Context, id domain.ID) (domain.Message, error) {
	row, err := s.queries.GetMessage(ctx, string(id))
	if err != nil {
		return domain.Message{}, notFound(err)
	}
	return decodeMessage(row)
}
func decodeEdit(row dbgen.MessageEditCommand) (domain.PendingMessageEdit, error) {
	var out domain.PendingMessageEdit
	err := json.Unmarshal([]byte(row.Payload), &out)
	if err == nil && (string(out.Command.ID) != row.ID || string(out.Command.RevisionID) != row.RevisionID || out.Command.State != row.State || string(out.Command.MessageID) != row.MessageID || string(out.Command.AccountID) != row.AccountID || string(out.Command.ConversationID) != row.ConversationID) {
		err = application.ErrProtocolUnavailable
	}
	return out, err
}
func (s *Store) MessageEdit(ctx context.Context, id domain.ID) (domain.PendingMessageEdit, error) {
	row, err := s.queries.GetMessageEditCommand(ctx, string(id))
	if err != nil {
		return domain.PendingMessageEdit{}, notFound(err)
	}
	return decodeEdit(row)
}
func replayEdit(ctx context.Context, q *dbgen.Queries, id domain.ID, digest string) (*domain.MessageEditCommand, error) {
	op, err := q.GetOperation(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if op.Kind != "edit_message" || op.Fingerprint != digest {
		return nil, application.ErrIdempotencyConflict
	}
	row, err := q.GetMessageEditCommand(ctx, string(id))
	if err != nil {
		return nil, notFound(err)
	}
	p, err := decodeEdit(row)
	return &p.Command, err
}
func (s *Store) ReplayMessageEdit(ctx context.Context, id domain.ID, digest string) (*domain.MessageEditCommand, error) {
	return replayEdit(ctx, s.queries, id, digest)
}
func (s *Store) AcceptMessageEdit(ctx context.Context, in application.MessageEditMutation) (application.MessageEditResult, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return application.MessageEditResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return application.MessageEditResult{}, err
	}
	p := in.Pending
	replay, err := replayEdit(ctx, q, p.Command.ID, in.Fingerprint)
	if err != nil {
		return application.MessageEditResult{}, err
	}
	if replay != nil {
		return application.MessageEditResult{Command: *replay, Replayed: true}, nil
	}
	candidate, err := readEditCandidate(ctx, q, p.Command.MessageID, false, "")
	if err != nil {
		return application.MessageEditResult{}, err
	}
	target := candidate.Target
	if !target.Eligible || target.CurrentRevisionID != p.Command.ExpectedRevisionID || target.MessageID != p.Command.MessageID || target.AccountID != p.Command.AccountID || target.ConversationID != p.Command.ConversationID || p.Plan.TargetMessageID != target.MessageID || p.Plan.OriginalWireID != candidate.Source.ProviderID {
		return application.MessageEditResult{}, application.EditConflict{Target: target}
	}
	if _, err = q.GetMessage(ctx, string(p.Command.ID)); err == nil {
		return application.MessageEditResult{}, application.ErrIdempotencyConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return application.MessageEditResult{}, err
	}
	if _, err = q.FindProviderMessage(ctx, dbgen.FindProviderMessageParams{AccountID: string(p.Command.AccountID), ConversationID: string(p.Command.ConversationID), ProviderID: string(p.Command.ID), IsOwn: 1}); err == nil {
		return application.MessageEditResult{}, application.ErrIdempotencyConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return application.MessageEditResult{}, err
	}
	if err = checkEditRoomIntent(ctx, q, p); err != nil {
		return application.MessageEditResult{}, err
	}
	_, err = operation(ctx, q, application.Operation{ID: p.Command.ID, ResourceID: p.Command.ID, Kind: "edit_message", CreatedAt: p.Command.CreatedAt}, in.Fingerprint, p.Command)
	if err != nil {
		return application.MessageEditResult{}, err
	}
	err = q.InsertMessageEditCommand(ctx, dbgen.InsertMessageEditCommandParams{ID: string(p.Command.ID), RevisionID: string(p.Command.RevisionID), MessageID: string(p.Command.MessageID), AccountID: string(p.Command.AccountID), ConversationID: string(p.Command.ConversationID), State: p.Command.State, Payload: jsonText(p), NextAttemptAt: stamp(p.Command.CreatedAt)})
	if err != nil {
		return application.MessageEditResult{}, err
	}
	return application.MessageEditResult{Command: p.Command}, tx.Commit()
}
func checkEditRoomIntent(ctx context.Context, q *dbgen.Queries, p domain.PendingMessageEdit) error {
	crow, err := q.GetConversation(ctx, string(p.Command.ConversationID))
	if err != nil {
		return err
	}
	c, err := decodeConversation(crow)
	if err != nil {
		return err
	}
	if c.Kind != "room" {
		return nil
	}
	intent, err := q.GetRoomIntent(ctx, string(c.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return application.ErrStateConflict
	}
	if err != nil {
		return err
	}
	if intent.Active != 1 || intent.RevisionID != string(p.Plan.RoomIntentRevisionID) {
		return application.ErrStateConflict
	}
	return nil
}
func (s *Store) PendingMessageEdits(ctx context.Context, at time.Time) ([]domain.PendingMessageEdit, error) {
	rows, err := s.queries.PendingMessageEditCommands(ctx, stamp(at))
	if err != nil {
		return nil, err
	}
	return decodeEdits(rows)
}
func (s *Store) InterruptedMessageEdits(ctx context.Context) ([]domain.PendingMessageEdit, error) {
	rows, err := s.queries.InterruptedMessageEditCommands(ctx)
	if err != nil {
		return nil, err
	}
	return decodeEdits(rows)
}
func decodeEdits(rows []dbgen.MessageEditCommand) ([]domain.PendingMessageEdit, error) {
	out := make([]domain.PendingMessageEdit, 0, len(rows))
	for _, row := range rows {
		p, err := decodeEdit(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func writeEdit(ctx context.Context, q *dbgen.Queries, p domain.PendingMessageEdit, next time.Time, attempt int64) error {
	return q.UpdateMessageEditCommand(ctx, dbgen.UpdateMessageEditCommandParams{RevisionID: string(p.Command.RevisionID), State: p.Command.State, Payload: jsonText(p), NextAttemptAt: stamp(next), AttemptCount: attempt, ID: string(p.Command.ID)})
}
func (s *Store) ClaimMessageEdit(ctx context.Context, id, revision domain.ID, at time.Time) (domain.PendingMessageEdit, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.PendingMessageEdit{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return domain.PendingMessageEdit{}, err
	}
	row, err := q.GetMessageEditCommand(ctx, string(id))
	if err != nil {
		return domain.PendingMessageEdit{}, notFound(err)
	}
	p, err := decodeEdit(row)
	if err != nil {
		return p, err
	}
	if p.Command.State != "queued" {
		return p, application.ErrStateConflict
	}
	candidate, err := readEditCandidate(ctx, q, p.Command.MessageID, false, id)
	if err != nil {
		return p, err
	}
	reason := ""
	if !candidate.Target.Eligible {
		reason = candidate.Target.Reason
	} else if candidate.Target.CurrentRevisionID != p.Command.ExpectedRevisionID {
		reason = "stale_content"
	}
	if err = checkEditRoomIntent(ctx, q, p); err != nil {
		if !errors.Is(err, application.ErrStateConflict) {
			return p, err
		}
		reason = "session_changed"
	}
	p.Command.RevisionID = revision
	p.Command.UpdatedAt = at
	if reason != "" {
		p.Command.State = "failed"
		p.Command.LastErrorCode = reason
		if err = writeEdit(ctx, q, p, at, 0); err != nil {
			return p, err
		}
		if err = tx.Commit(); err != nil {
			return p, err
		}
		return p, application.ErrStateConflict
	}
	p.Command.State = "dispatching"
	if err = writeEdit(ctx, q, p, at, 1); err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (s *Store) FinishMessageEdit(ctx context.Context, id domain.ID, state string, revision domain.ID, at, next time.Time, reason string) (domain.MessageEditCommand, error) {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return domain.MessageEditCommand{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return domain.MessageEditCommand{}, err
	}
	row, err := q.GetMessageEditCommand(ctx, string(id))
	if err != nil {
		return domain.MessageEditCommand{}, notFound(err)
	}
	p, err := decodeEdit(row)
	if err != nil {
		return p.Command, err
	}
	if p.Command.State != "dispatching" {
		return p.Command, application.ErrStateConflict
	}
	if state != "queued" && state != "submitted" && state != "unknown" && state != "failed" {
		return p.Command, application.ErrInvalid
	}
	switch state {
	case "submitted":
		if err = materializeEdit(ctx, q, p, at); err != nil {
			return p.Command, err
		}
	case "unknown", "queued":
		observed, err := hasVerifiedEditEcho(ctx, q, p)
		if err != nil {
			return p.Command, err
		}
		if observed {
			if err = materializeEdit(ctx, q, p, at); err != nil {
				return p.Command, err
			}
			state = "submitted"
			reason = ""
		}
	}
	p.Command.State = state
	p.Command.RevisionID = revision
	p.Command.UpdatedAt = at
	p.Command.LastErrorCode = reason
	if err = writeEdit(ctx, q, p, next, 0); err != nil {
		return p.Command, err
	}
	return p.Command, tx.Commit()
}

func hasVerifiedEditEcho(ctx context.Context, q *dbgen.Queries, p domain.PendingMessageEdit) (bool, error) {
	row, err := q.FindProviderMessage(ctx, dbgen.FindProviderMessageParams{AccountID: string(p.Command.AccountID), ConversationID: string(p.Command.ConversationID), ProviderID: string(p.Command.ID), IsOwn: 1})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	m, err := decodeMessage(row)
	if err != nil {
		return false, err
	}
	if m.OriginalText != p.Command.OriginalText {
		return false, nil
	}
	facts, err := q.GetCorrectionFacts(ctx, row.ID)
	if err != nil {
		return false, err
	}
	if len(facts) != 1 {
		return false, nil
	}
	f := facts[0]
	return f.Scope == p.Plan.Scope && f.Author == p.Plan.Author && f.RemoteID == string(p.Command.ID) && f.TargetID == p.Plan.OriginalWireID && f.SourceHash == bodyHash(p.Command.OriginalText), nil
}

func (s *Store) ObserveMessageEdit(ctx context.Context, account domain.ID, provider string, proof domain.MessageCorrection, text string, revision domain.ID, at time.Time) error {
	id, err := domain.ParseID(provider)
	if err != nil {
		return nil
	}
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	row, err := q.GetMessageEditCommand(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := decodeEdit(row)
	if err != nil {
		return err
	}
	if p.Command.AccountID != account || p.Command.OriginalText != text || p.Command.State == "submitted" || proof.Scope != p.Plan.Scope || proof.Author != p.Plan.Author || proof.ID != string(id) || proof.TargetID != p.Plan.OriginalWireID {
		return nil
	}
	observed, err := hasVerifiedEditEcho(ctx, q, p)
	if err != nil || !observed {
		return err
	}
	if err = materializeEdit(ctx, q, p, at); err != nil {
		return err
	}
	if p.Command.State == "failed" {
		return tx.Commit()
	}
	p.Command.State = "submitted"
	p.Command.RevisionID = revision
	p.Command.UpdatedAt = at
	p.Command.LastErrorCode = ""
	if err = writeEdit(ctx, q, p, at, 0); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RejectMessageEdit(ctx context.Context, id, revision domain.ID, at time.Time) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	row, err := q.GetMessageEditCommand(ctx, string(id))
	if err != nil {
		return notFound(err)
	}
	p, err := decodeEdit(row)
	if err != nil {
		return err
	}
	if p.Command.State != "submitted" && p.Command.State != "dispatching" && p.Command.State != "unknown" {
		return nil
	}
	p.Command.State = "failed"
	p.Command.LastErrorCode = "protocol_rejected"
	p.Command.RevisionID = revision
	p.Command.UpdatedAt = at
	if err = writeEdit(ctx, q, p, at, 0); err != nil {
		return err
	}
	base, err := q.GetMessage(ctx, string(p.Command.MessageID))
	if err != nil {
		return err
	}
	message, err := decodeMessage(base)
	if err != nil {
		return err
	}
	if _, err = emitCanonicalMessageEvent(ctx, q, "message.updated", message, at); err != nil {
		return err
	}
	return tx.Commit()
}
func materializeEdit(ctx context.Context, q *dbgen.Queries, p domain.PendingMessageEdit, at time.Time) error {
	baseRow, err := q.GetMessage(ctx, string(p.Command.MessageID))
	if err != nil {
		return err
	}
	base, err := decodeMessage(baseRow)
	if err != nil {
		return err
	}
	base.Correction = &domain.MessageCorrection{Scope: p.Plan.Scope, Author: p.Plan.Author, ID: p.Plan.OriginalWireID}
	if err = recordCorrectionMetadata(ctx, q, string(base.ID), base); err != nil {
		return err
	}
	existing, err := q.FindProviderMessage(ctx, dbgen.FindProviderMessageParams{AccountID: string(p.Command.AccountID), ConversationID: string(p.Command.ConversationID), ProviderID: string(p.Command.ID), IsOwn: 1})
	proof := &domain.MessageCorrection{Scope: p.Plan.Scope, Author: p.Plan.Author, ID: string(p.Command.ID), TargetID: p.Plan.OriginalWireID}
	if err == nil {
		source, err := decodeMessage(existing)
		if err != nil {
			return err
		}
		if source.OriginalText != p.Command.OriginalText || source.AccountID != p.Command.AccountID || source.ConversationID != p.Command.ConversationID {
			return application.ErrStateConflict
		}
		source.Correction = proof
		return recordCorrectionMetadata(ctx, q, existing.ID, source)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	source := domain.Message{ID: p.Command.ID, RevisionID: p.ContentRevisionID, AccountID: p.Command.AccountID, ConversationID: p.Command.ConversationID, Sender: base.Sender, OriginalText: p.Command.OriginalText, CreatedAt: at, IsOwn: true, SendState: "submitted", ProviderID: string(p.Command.ID), Correction: proof}
	return insertMessage(ctx, q, source)
}

// localCorrectionParent uses the immutable acceptance receipt to prove which
// content was edited, independently from the original stanza named on the wire.
func localCorrectionParent(ctx context.Context, q *dbgen.Queries, source domain.Message, scope, author, remote, target string) (domain.ID, domain.ID, bool, error) {
	if !source.IsOwn || source.ProviderID != remote || target == "" {
		return "", "", false, nil
	}
	row, err := q.GetMessageEditCommand(ctx, remote)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	p, err := decodeEdit(row)
	if err != nil {
		return "", "", false, err
	}
	if p.Command.AccountID != source.AccountID || p.Command.ConversationID != source.ConversationID || p.Command.OriginalText != source.OriginalText || p.Plan.Scope != scope || p.Plan.Author != author || p.Plan.OriginalWireID != target || p.Plan.TargetMessageID != p.Command.MessageID {
		return "", "", false, nil
	}
	op, err := q.GetOperation(ctx, remote)
	if err != nil {
		return "", "", false, err
	}
	var accepted domain.MessageEditCommand
	if op.Kind != "edit_message" || op.ResourceID != remote || json.Unmarshal([]byte(op.Result), &accepted) != nil || accepted.ID != p.Command.ID || accepted.MessageID != p.Command.MessageID || accepted.AccountID != p.Command.AccountID || accepted.ConversationID != p.Command.ConversationID || accepted.ExpectedRevisionID != p.Command.ExpectedRevisionID || accepted.OriginalText != p.Command.OriginalText || !accepted.CreatedAt.Equal(p.Command.CreatedAt) {
		return "", "", false, application.ErrProtocolUnavailable
	}
	parent, err := q.FirstContentRevisionSource(ctx, string(accepted.ExpectedRevisionID))
	if err != nil {
		return "", "", false, application.ErrProtocolUnavailable
	}
	return accepted.MessageID, domain.ID(parent), true, nil
}
