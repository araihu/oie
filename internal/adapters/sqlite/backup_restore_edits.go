package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/araihu/oie/internal/adapters/sqlite/dbgen"
	"github.com/araihu/oie/internal/domain"
	"github.com/google/uuid"
)

func reconcileRestoredEdits(ctx context.Context, tx *sql.Tx, q *dbgen.Queries, now time.Time) error {
	rows, err := tx.QueryContext(ctx, restoreScript("edit_evidence"))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var commandID string
		var interrupted bool
		var witnessID sql.NullString
		if err := rows.Scan(&commandID, &interrupted, &witnessID); err != nil {
			return err
		}
		command, err := q.GetMessageEditCommand(ctx, commandID)
		if err != nil {
			return err
		}
		edit, err := decodeEdit(command)
		if err != nil {
			return err
		}
		state, reason := "", ""
		if interrupted {
			state, reason = "unknown", "interrupted"
		}
		if witnessID.Valid {
			verified, err := verifyRestoredEditWitness(ctx, q, edit, witnessID.String)
			if err != nil {
				return err
			}
			if verified {
				if _, err := recordEditRoot(ctx, q, edit); err != nil {
					return err
				}
				if edit.Command.State != "failed" {
					state, reason = "submitted", ""
				}
			}
		}
		if state == "" {
			continue
		}
		revision, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if err := recordEditOutcome(ctx, q, edit, state, reason, domain.ID(revision.String()), now); err != nil {
			return err
		}
	}
	return rows.Err()
}

func verifyRestoredEditWitness(ctx context.Context, q *dbgen.Queries, edit domain.PendingMessageEdit, id string) (bool, error) {
	row, err := q.GetMessage(ctx, id)
	if err != nil {
		return false, err
	}
	witness, err := decodeMessage(row)
	if err != nil {
		return false, err
	}
	verified, err := verifiedEditSource(ctx, q, edit, witness)
	if err != nil || !verified {
		return false, err
	}
	root, _, accepted, err := localCorrectionParent(ctx, q, witness, edit.Plan.Scope, edit.Plan.Author, string(edit.Command.ID), edit.Plan.OriginalWireID)
	return accepted && root == edit.Command.MessageID, err
}
