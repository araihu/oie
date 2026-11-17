package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func reconcileRestoredSendEvidence(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, now time.Time) error {
	if _, err := tx.ExecContext(ctx, restoreScript("send_evidence")); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, restoreScript("active_send_evidence"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, state string
		var when, payload sql.NullString
		var timeline, submittedAt sql.NullInt64
		if err := rows.Scan(&id, &state, &when, &payload, &timeline, &submittedAt); err != nil {
			return err
		}
		messageRow, err := q.GetMessage(ctx, id)
		if err != nil {
			return err
		}
		message, err := decodeMessage(messageRow)
		if err != nil {
			return err
		}
		if state == "submitted" {
			var submitted domain.Message
			if err := json.Unmarshal([]byte(payload.String), &submitted); err != nil {
				return err
			}
			if message.SendState == "unknown" && !matchesUncertainNativeComposition(message, submitted) {
				continue
			}
			if !matchesSubmittedComposition(message, submitted) {
				state = "unknown"
			} else if submitted.ID == message.ID && len(message.Attachments) != 0 && message.AttachmentWireText == "" {
				message.AttachmentWireText = submitted.AttachmentWireText
			}
		}
		at := now
		if when.Valid {
			at, err = time.Parse(time.RFC3339Nano, when.String)
			if err != nil {
				return err
			}
		}
		publishSchedule := message.ScheduledAt != nil && (state == "submitted" || state == "failed" || state == "unknown")
		confirmedTimeline := message.SendState == "unknown" && state == "submitted" && timeline.Valid
		if confirmedTimeline || (message.TimelineAt == nil && (timeline.Valid || publishSchedule)) {
			// Place the restored send at its historical time. The restore summary
			// updates the conversation under its existing activity and revision.
			millis := at.UnixMilli()
			if timeline.Valid {
				millis = timeline.Int64
			}
			if err := q.SetMessageTimelineAt(ctx, dbgen.SetMessageTimelineAtParams{ID: id, TimelineAtMs: &millis}); err != nil {
				return err
			}
			setTimelineTime(&message, &millis)
			if _, err := tx.ExecContext(ctx, "UPDATE restore_chats SET history_changed=1 WHERE id=?", message.ConversationID); err != nil {
				return err
			}
		}
		revision, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if _, err := updateMessage(ctx, q, message, state, domain.ID(revision.String()), at, now, false); err != nil {
			return err
		}
		if submittedAt.Valid {
			if err := q.SetMessageSubmittedAt(ctx, dbgen.SetMessageSubmittedAtParams{ID: id, SubmittedAtMs: &submittedAt.Int64}); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func matchesSubmittedComposition(current, submitted domain.Message) bool {
	if current.ID != submitted.ID {
		// An independently witnessed echo records the wire text. For a plain
		// send, that text is the same proof used to reconcile a live echo.
		return current.OutboundReply == nil && len(current.Mentions) == 0 &&
			len(current.Attachments) == 0 && current.AttachmentWireText == "" &&
			submitted.OutboundReply == nil && len(submitted.Mentions) == 0 &&
			len(submitted.Attachments) == 0 && len(submitted.FileReferences) == 0 &&
			len(submitted.AudioURLs) == 0 && len(submitted.FileDigests) == 0 && len(submitted.MediaThumbnails) == 0 &&
			current.MatchesWireBody(submitted.OriginalText)
	}
	return current.OriginalText == submitted.OriginalText &&
		reflect.DeepEqual(current.Composition, submitted.Composition) &&
		reflect.DeepEqual(current.Attachments, submitted.Attachments) &&
		reflect.DeepEqual(current.Mentions, submitted.Mentions) &&
		reflect.DeepEqual(current.OutboundReply, submitted.OutboundReply)
}

func matchesUncertainNativeComposition(current, submitted domain.Message) bool {
	return current.ID == submitted.ID && matchesSubmittedComposition(current, submitted) &&
		(current.AttachmentWireText == "" || current.AttachmentWireText == submitted.AttachmentWireText)
}

func mergeRestoredQueue(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, engine domain.ID, hasRecovered, hasHistory bool) error {
	if _, err := tx.ExecContext(ctx, restoreScript("pending"), sql.Named("source_engine", string(engine))); err != nil {
		return err
	}
	if hasRecovered {
		if _, err := tx.ExecContext(ctx, restoreScript("recovered_pending")); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, restoreScript("merge_pending")); err != nil {
		return err
	}
	if hasHistory {
		if _, err := tx.ExecContext(ctx, restoreScript("recovered_revisions")); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, restoreScript("pending_updates"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var candidate dbgen.RecoveredSend
		if err := rows.Scan(&candidate.MessageID, &candidate.ConversationID, &candidate.SourceEngineID,
			&candidate.RevisionID, &candidate.OriginalState, &candidate.AttemptCount, &candidate.ScheduledAt,
			&candidate.Payload, &candidate.CreatedAtMs); err != nil {
			return err
		}
		if err := mergeRecoveredSend(ctx, q, candidate); err != nil {
			return err
		}
	}
	return rows.Err()
}

func mergeRecoveredSend(ctx context.Context, q *dbgen.Queries, candidate dbgen.RecoveredSend) error {
	current, err := q.GetRecoveredSend(ctx, candidate.MessageID)
	if err != nil {
		return err
	}
	if current.ConversationID != candidate.ConversationID || current.SourceEngineID != candidate.SourceEngineID {
		return errors.New("backup includes conflicting ownership for a recovered send")
	}
	rows, err := q.ListRecoveredSendRevisions(ctx, candidate.MessageID)
	if err != nil {
		return err
	}
	group := restoreRevisionSet{entity: domain.ID(candidate.MessageID), payloads: make(map[domain.ID]string, len(rows))}
	for _, row := range rows {
		at, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err != nil {
			return err
		}
		r := domain.Revision{ID: domain.ID(row.ID), EntityID: group.entity, CreatedAt: at}
		if err := json.Unmarshal([]byte(row.Parents), &r.Parents); err != nil {
			return err
		}
		group.revisions = append(group.revisions, r)
		group.payloads[r.ID] = row.Payload
	}
	// Archives written before recovered revision history was stored have only
	// the snapshot head. Keep that revision as an independent root.
	for _, snapshot := range []dbgen.RecoveredSend{current, candidate} {
		id := domain.ID(snapshot.RevisionID)
		if _, found := group.payloads[id]; found {
			continue
		}
		at := time.UnixMilli(snapshot.CreatedAtMs).UTC()
		if err := q.PutRecoveredSendRevision(ctx, dbgen.PutRecoveredSendRevisionParams{
			ID: snapshot.RevisionID, MessageID: snapshot.MessageID, Parents: "[]",
			Payload: snapshot.Payload, CreatedAt: stamp(at),
		}); err != nil {
			return err
		}
		group.revisions = append(group.revisions, domain.Revision{ID: id, EntityID: group.entity, CreatedAt: at})
		group.payloads[id] = snapshot.Payload
	}
	winner, err := domain.CurrentRevision(group.revisions)
	if err != nil {
		return err
	}
	payload := group.payloads[winner.ID]
	var message domain.Message
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		return err
	}
	if message.ID != group.entity || message.RevisionID != winner.ID || string(message.ConversationID) != candidate.ConversationID {
		return errors.New("backup has an invalid recovered send revision")
	}
	return q.UpdateRecoveredSend(ctx, dbgen.UpdateRecoveredSendParams{
		MessageID: candidate.MessageID, RevisionID: string(winner.ID), Payload: payload,
		OriginalState: combinedSendState(current.OriginalState, candidate.OriginalState),
		AttemptCount:  max(current.AttemptCount, candidate.AttemptCount), ScheduledAt: scheduleStamp(message.ScheduledAt),
	})
}

func combinedSendState(current, incoming string) string {
	if current == "unknown" || incoming == "unknown" {
		return "unknown"
	}
	if current == "dispatching" || incoming == "dispatching" {
		return "dispatching"
	}
	return "queued"
}
