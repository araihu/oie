package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
)

type RecoveredSend struct {
	MessageID       domain.ID  `json:"messageId"`
	RevisionID      domain.ID  `json:"revisionId"`
	AccountID       domain.ID  `json:"accountId"`
	SourceEngineID  domain.ID  `json:"sourceEngineId"`
	Destination     string     `json:"destination"`
	OriginalState   string     `json:"originalState"`
	AttemptCount    int64      `json:"attemptCount"`
	Text            string     `json:"text"`
	AttachmentNames []string   `json:"attachmentNames"`
	ScheduledAt     *time.Time `json:"scheduledAt,omitempty"`
}

func (s *Store) RecoveredSends(ctx context.Context) ([]RecoveredSend, error) {
	rows, err := s.read.ListRecoveredSends(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]RecoveredSend, 0, len(rows))
	for _, row := range rows {
		var m domain.Message
		if err := json.Unmarshal([]byte(row.Payload), &m); err != nil {
			return nil, err
		}
		item := RecoveredSend{MessageID: domain.ID(row.MessageID), RevisionID: domain.ID(row.RevisionID), AccountID: domain.ID(row.AccountID), SourceEngineID: domain.ID(row.SourceEngineID), Destination: row.Address, OriginalState: row.OriginalState, AttemptCount: row.AttemptCount, Text: domain.DisplayBody(m), ScheduledAt: m.ScheduledAt, AttachmentNames: []string{}}
		for _, f := range m.Attachments {
			item.AttachmentNames = append(item.AttachmentNames, f.FileName)
		}
		items = append(items, item)
	}
	return items, nil
}

// ReviewRecoveredSend discards a reviewed item or returns a known-unsent send
// to its original engine. Callers must explicitly confirm that it was not sent
// after the snapshot before requesting resume.
func (s *Store) ReviewRecoveredSend(ctx context.Context, id, revision domain.ID, resume bool, now time.Time) error {
	tx, q, err := begin(ctx, s)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := q.AcquireWriteLock(ctx); err != nil {
		return err
	}
	pending, err := q.GetRecoveredSend(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("no paused send with this message ID")
	}
	if err != nil {
		return err
	}
	if pending.RevisionID != string(revision) {
		return errors.New("the paused send changed; review the current revision")
	}
	if resume {
		if err := s.resumeRecoveredSend(ctx, q, pending, now); err != nil {
			return err
		}
	} else if err := q.DiscardRecoveredSend(ctx, string(id)); err != nil {
		return err
	}
	if err := q.DeleteRecoveredSend(ctx, string(id)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) resumeRecoveredSend(ctx context.Context, q *dbgen.Queries, recovered dbgen.RecoveredSend, now time.Time) error {
	engine, err := q.Snapshot(ctx)
	if err != nil {
		return err
	}
	if recovered.SourceEngineID != engine.ID {
		return errors.New("this profile does not own the original send")
	}
	if recovered.OriginalState != "queued" || recovered.AttemptCount != 0 {
		return errors.New("the original send may have been transmitted and needs reconciliation")
	}
	var m domain.Message
	if err := json.Unmarshal([]byte(recovered.Payload), &m); err != nil {
		return err
	}
	chatRow, err := q.GetConversation(ctx, recovered.ConversationID)
	if err != nil {
		return err
	}
	chat, err := decodeConversation(chatRow)
	if err != nil {
		return err
	}
	if string(m.ID) != recovered.MessageID || string(m.RevisionID) != recovered.RevisionID || m.ConversationID != chat.ID ||
		m.AccountID != chat.AccountID || !m.IsOwn || m.SendState != "queued" || m.DispatchStarted ||
		m.IsWithdrawn() || m.ProviderID != string(m.ID) || m.Sender.ID != chat.AccountID || m.CreatedAt.IsZero() ||
		scheduleStamp(m.ScheduledAt) != recovered.ScheduledAt {
		return errors.New("the recovered composition is invalid")
	}
	if chat.RemovedFromChats || chat.Request != "" {
		return errors.New("open and accept the destination conversation before resuming")
	}
	if chat.Kind == "room" {
		intent, err := q.GetRoomIntent(ctx, recovered.ConversationID)
		if err != nil || intent.Active == 0 {
			return errors.New("join the destination room before resuming")
		}
	}
	hasEvidence, err := q.RecoveredSendHasDestinationEvidence(ctx, dbgen.RecoveredSendHasDestinationEvidenceParams{
		MessageID: recovered.MessageID, ConversationID: recovered.ConversationID, AccountID: chatRow.AccountID,
	})
	if err != nil {
		return err
	}
	if hasEvidence != 0 {
		return errors.New("the destination already has evidence for this send")
	}
	observed, err := hasOwnTransmissionEvidence(ctx, q, m)
	if err != nil {
		return err
	}
	if observed {
		return errors.New("the destination already has evidence for this send")
	}
	if err := checkPendingReplyTarget(ctx, q, &m); err != nil {
		safe, lookupErr := recoveredReplyTargetExpired(ctx, q, m)
		if lookupErr != nil {
			return lookupErr
		}
		if !safe {
			return err
		}
	}
	for _, attachment := range m.Attachments {
		if err := validateRecoveredAttachment(ctx, q, s.dir, chat.ID, attachment); err != nil {
			return err
		}
	}
	if err := insertMessage(ctx, q, m); err != nil {
		return err
	}
	if m.TimelineAt != nil {
		ms := m.TimelineAt.UnixMilli()
		if err := q.SetMessageTimelineAt(ctx, dbgen.SetMessageTimelineAtParams{ID: recovered.MessageID, TimelineAtMs: &ms}); err != nil {
			return err
		}
	}
	if err := q.PreserveResumedRevision(ctx, recovered.MessageID); err != nil {
		return err
	}
	if err := q.InsertOutbox(ctx, dbgen.InsertOutboxParams{MessageID: recovered.MessageID, NextAttemptAt: eligibleAt(m, now), ScheduledAt: recovered.ScheduledAt}); err != nil {
		return err
	}
	if err := q.RestoreReviewedSendReceipts(ctx, recovered.MessageID); err != nil {
		return err
	}
	return refreshConversationSummary(ctx, q, chat.ID, now)
}

func recoveredReplyTargetExpired(ctx context.Context, q *dbgen.Queries, m domain.Message) (bool, error) {
	plan := m.OutboundReply
	if plan == nil || plan.TargetID == "" || plan.Reference != nil || plan.Excerpt != "" ||
		plan.SenderName != "" || len(plan.Fallback) != 0 || m.OriginalText != plan.BodyText || m.AttachmentWireText != "" {
		return false, nil
	}
	if _, err := q.GetMessage(ctx, string(plan.TargetID)); !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return q.IsExpiredMessage(ctx, string(plan.TargetID))
}

func validateRecoveredAttachment(ctx context.Context, q *dbgen.Queries, dir string, conversation domain.ID, file domain.StagedAttachment) error {
	if _, err := domain.ParseID(string(file.ID)); err != nil {
		return err
	}
	row, err := q.GetStagedAttachment(ctx, string(file.ID))
	if err != nil || row.ConversationID != string(conversation) || attachmentFromRow(row) != file {
		return fmt.Errorf("attachment %q is unavailable", file.FileName)
	}
	if err := checkRestoredMedia(ctx, filepath.Join(dir, "media", "staged", string(file.ID)), backupFile{Name: file.FileName, Size: file.Size, SHA256: file.SHA256}); err != nil {
		return fmt.Errorf("attachment %q is unavailable: %w", file.FileName, err)
	}
	return nil
}

func scrubRecoveredSendQuotes(ctx context.Context, q *dbgen.Queries, conversation, excluded string) error {
	if err := q.ScrubRecoveredSendPayloads(ctx, dbgen.ScrubRecoveredSendPayloadsParams{OwnerID: conversation, ExpiredIds: excluded}); err != nil {
		return err
	}
	if err := q.ScrubRecoveredSendRevisions(ctx, dbgen.ScrubRecoveredSendRevisionsParams{OwnerID: conversation, ExpiredIds: excluded}); err != nil {
		return err
	}
	return q.ScrubRecoveredSendReceipts(ctx, dbgen.ScrubRecoveredSendReceiptsParams{OwnerID: conversation, ExpiredIds: excluded})
}

func rememberOwnOrigin(ctx context.Context, q *dbgen.Queries, chat domain.Conversation, m domain.Message) error {
	if !m.IsOwn || m.OwnOriginID == "" {
		return nil
	}
	return q.RememberOwnOrigin(ctx, dbgen.RememberOwnOriginParams{
		MessageID: string(m.OwnOriginID), AccountID: string(chat.AccountID),
		Address: chat.Address, Kind: chat.Kind,
	})
}

func hasOwnTransmissionEvidence(ctx context.Context, q *dbgen.Queries, m domain.Message) (bool, error) {
	observed, err := q.HasOwnTransmissionEvidence(ctx, dbgen.HasOwnTransmissionEvidenceParams{
		AccountID: string(m.AccountID), ConversationID: string(m.ConversationID), MessageID: string(m.ID),
	})
	return observed != 0, err
}
