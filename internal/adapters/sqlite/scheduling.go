package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/application"
	"github.com/araihu/oie/internal/domain"
)

func scheduleStamp(at *time.Time) string {
	if at == nil {
		return ""
	}
	return stamp(*at)
}

func eligibleAt(m domain.Message, next time.Time) string {
	if m.SendState == "queued" && m.ScheduledAt != nil && m.ScheduledAt.After(next) {
		return stamp(*m.ScheduledAt)
	}
	return stamp(next)
}

func (s *Store) ScheduledMessages(ctx context.Context, in application.ScheduledQuery) ([]application.ScheduledMessage, error) {
	var after string
	if !in.AfterTime.IsZero() {
		after = stamp(in.AfterTime)
	}
	tx, q, err := begin(ctx, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	conversations := "[]"
	if len(in.ConversationIDs) > 0 {
		conversations = jsonText(in.ConversationIDs)
	}
	rows, err := q.ListScheduledMessages(ctx, dbgen.ListScheduledMessagesParams{ConversationIds: conversations, AfterTime: after, AfterID: string(in.AfterID), PageLimit: int64(in.Limit)})
	if err != nil {
		return nil, err
	}
	out := make([]application.ScheduledMessage, 0, len(rows))
	for _, row := range rows {
		m, err := decodeMessage(row.Message)
		if err != nil {
			return nil, err
		}
		if err = hydrateReply(ctx, q, &m); err != nil {
			return nil, err
		}
		c, err := decodeConversation(row.Conversation)
		if err != nil {
			return nil, err
		}
		out = append(out, application.ScheduledMessage{Message: m, Conversation: c})
	}
	return out, tx.Commit()
}

func (s *Store) UpdatePendingMessage(ctx context.Context, in application.PendingMutation) (application.MessageResult, error) {
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
	id := string(in.Operation.ResourceID)
	row, err := q.GetMessage(ctx, id)
	if err != nil {
		return application.MessageResult{}, notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return application.MessageResult{}, err
	}
	if !m.IsOwn || m.SendState != "queued" || m.RevisionID != in.ExpectedRevisionID || (in.SendNow && m.ScheduledAt == nil) {
		return application.MessageResult{}, application.StateConflict{Current: m}
	}
	outbox, err := q.GetOutbox(ctx, id)
	if err != nil {
		return application.MessageResult{}, err
	}
	if outbox.AttemptCount != 0 {
		return application.MessageResult{}, application.StateConflict{Current: m}
	}
	next := in.Operation.CreatedAt
	hideFromTimeline := in.ScheduledAt != nil && m.ScheduledAt == nil
	switch {
	case in.SendNow:
		m.ScheduledAt = nil
	case in.ScheduledAt != nil:
		m.ScheduledAt = in.ScheduledAt
		if m.TimelineAt != nil {
			if err = q.SetMessageTimelineAt(ctx, dbgen.SetMessageTimelineAtParams{ID: id}); err != nil {
				return application.MessageResult{}, err
			}
			m.TimelineAt = nil
		}
	default:
		next, err = time.Parse(time.RFC3339Nano, outbox.NextAttemptAt)
		if err != nil {
			return application.MessageResult{}, err
		}
	}
	if changed := in.Composition; changed != nil {
		if changed.OutboundReply != nil && (in.ExpectedReplyRevisionID != "" || m.OutboundReply == nil || m.OutboundReply.TargetID != changed.OutboundReply.TargetID) {
			if err = checkPendingReplyTarget(ctx, q, changed); err != nil {
				return application.MessageResult{}, err
			}
		}
		m.OriginalText, m.Composition, m.Attachments = changed.OriginalText, changed.Composition, changed.Attachments
		m.OutboundReply, m.Mentions = changed.OutboundReply, changed.Mentions
		m.ReplyReference, m.References = changed.ReplyReference, changed.References
		m.AttachmentWireText = ""
		if err = q.DeletePendingReplyMetadata(ctx, id); err != nil {
			return application.MessageResult{}, err
		}
		if err = q.InvalidateSharedContent(ctx, id); err != nil {
			return application.MessageResult{}, err
		}
	}
	if in.SendNow {
		if err = publishScheduledSend(ctx, q, &m, in.RevisionID, next); err != nil {
			return application.MessageResult{}, err
		}
	}
	m, err = updateMessage(ctx, q, m, "queued", in.RevisionID, in.Operation.CreatedAt, next, false)
	if err != nil {
		return application.MessageResult{}, err
	}
	if hideFromTimeline {
		if err = refreshConversationSummary(ctx, q, m.ConversationID, in.Operation.CreatedAt); err != nil {
			return application.MessageResult{}, err
		}
	}
	if in.Composition != nil {
		if err = recordReplyMetadata(ctx, q, id, m, false); err != nil {
			return application.MessageResult{}, err
		}
		if err = hydrateReply(ctx, q, &m); err != nil {
			return application.MessageResult{}, err
		}
	}
	if err = q.SetOperationResult(ctx, dbgen.SetOperationResultParams{ID: string(in.Operation.ID), ResourceID: id, Result: jsonText(m)}); err != nil {
		return application.MessageResult{}, err
	}
	return application.MessageResult{Message: m}, tx.Commit()
}

func (s *Store) RecordOriginalWireAttempt(ctx context.Context, id, revision domain.ID, attachmentBody string) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	row, err := q.GetMessage(ctx, string(id))
	if err != nil {
		return notFound(err)
	}
	m, err := decodeMessage(row)
	if err != nil {
		return err
	}
	if m.RevisionID != revision || m.SendState != "dispatching" {
		return application.StateConflict{Current: m}
	}
	if attachmentBody != "" {
		m.AttachmentWireText = attachmentBody
		if err = q.UpdateMessage(ctx, dbgen.UpdateMessageParams{ID: string(id), RevisionID: string(revision), State: m.SendState, Payload: jsonText(m)}); err != nil {
			return err
		}
	}
	// Upload claims can precede a text send that reaches the wire first.
	latest, err := q.LatestOwnMessage(ctx, string(m.ConversationID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if latest.ID != string(id) {
		if err = q.RecordOriginalTransmission(ctx, string(id)); err != nil {
			return err
		}
	}
	if err = q.MarkOutboxWireStarted(ctx, string(id)); err != nil {
		return err
	}
	return tx.Commit()
}

func attachmentFromRow(row dbgen.StagedAttachment) domain.StagedAttachment {
	return domain.StagedAttachment{ID: domain.ID(row.ID), FileName: row.FileName, MediaType: row.MediaType, Size: row.Size, SHA256: row.Sha256}
}

func (s *Store) StageAttachment(ctx context.Context, conversationID domain.ID, in domain.StagedAttachment, body io.Reader) (domain.StagedAttachment, bool, error) {
	dir := filepath.Join(s.dir, "media", "staged")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return in, false, err
	}
	tmp, err := os.CreateTemp(dir, ".incoming-")
	if err != nil {
		return in, false, err
	}
	defer func() { _ = os.Remove(tmp.Name()); _ = tmp.Close() }()
	hash := sha256.New()
	in.Size, err = io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(body, application.MaxStagedAttachmentSize+1))
	if err != nil {
		return in, false, err
	}
	if in.Size > application.MaxStagedAttachmentSize {
		return in, false, application.ErrAttachmentTooLarge
	}
	in.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if err = tmp.Sync(); err != nil {
		return in, false, err
	}
	if err = tmp.Close(); err != nil {
		return in, false, err
	}
	tx, q, err := begin(ctx, s)
	if err != nil {
		return in, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = q.AcquireWriteLock(ctx); err != nil {
		return in, false, err
	}
	row, err := q.GetStagedAttachment(ctx, string(in.ID))
	if err == nil {
		if row.ConversationID != string(conversationID) || row.FileName != in.FileName || row.Size != in.Size || row.Sha256 != in.SHA256 {
			return in, false, application.ErrIdempotencyConflict
		}
		return attachmentFromRow(row), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return in, false, err
	}
	if err = q.InsertStagedAttachment(ctx, dbgen.InsertStagedAttachmentParams{ID: string(in.ID), ConversationID: string(conversationID), FileName: in.FileName, MediaType: in.MediaType, Size: in.Size, Sha256: in.SHA256}); err != nil {
		return in, false, err
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, string(in.ID))); err != nil {
		return in, false, err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return in, false, err
	}
	err = errors.Join(parent.Sync(), parent.Close())
	if err != nil {
		return in, false, err
	}
	return in, false, tx.Commit()
}

func (s *Store) StagedAttachment(ctx context.Context, conversationID, id domain.ID) (domain.StagedAttachment, error) {
	row, err := s.queries.GetStagedAttachment(ctx, string(id))
	if err != nil {
		return domain.StagedAttachment{}, notFound(err)
	}
	if row.ConversationID != string(conversationID) {
		return domain.StagedAttachment{}, application.ErrNotFound
	}
	return attachmentFromRow(row), nil
}

func (s *Store) OpenStagedAttachment(ctx context.Context, conversationID, id domain.ID) (domain.StagedAttachment, io.ReadCloser, error) {
	file, err := s.StagedAttachment(ctx, conversationID, id)
	if err != nil {
		return file, nil, err
	}
	body, err := os.Open(filepath.Join(s.dir, "media", "staged", string(id)))
	if errors.Is(err, os.ErrNotExist) {
		err = application.ErrNotFound
	}
	return file, body, err
}
