package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Translation applies only to local relationships. Provider IDs, stanza
// references, operation IDs, revision IDs, parent lists and user text remain
// byte-for-byte stable even when they look like a local identifier.
type profileIDPair struct{ source, destination string }

func preparePortableArchive(ctx context.Context, tx *sql.Tx, path, scratch string) (string, func(), error) {
	if _, err := tx.ExecContext(ctx, restoreScript("portable_keys")); err != nil {
		return "", nil, err
	}
	rows, err := tx.QueryContext(ctx, restoreScript("portable_matches"))
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = rows.Close() }()
	forward, reverse := map[string]string{}, map[string]string{}
	var translations []profileIDPair
	for rows.Next() {
		var kind, source, dest string
		if err := rows.Scan(&kind, &source, &dest); err != nil {
			return "", nil, err
		}
		if prior, ok := forward[source]; ok && prior != dest {
			return "", nil, fmt.Errorf("review conflicting %s references for %s: destination %s or %s", kind, source, prior, dest)
		}
		if prior, ok := reverse[dest]; ok && prior != source {
			return "", nil, fmt.Errorf("review conflicting %s references for %s: sources %s and %s", kind, dest, prior, source)
		}
		if _, exists := forward[source]; !exists && source != dest {
			translations = append(translations, profileIDPair{source, dest})
		}
		forward[source], reverse[dest] = dest, source
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	var source, destination string
	err = tx.QueryRowContext(ctx, restoreScript("portable_conflicts")).Scan(&source, &destination)
	if err == nil {
		return "", nil, fmt.Errorf("review possible duplicate or conflicting XMPP evidence: archive message %s, destination message %s", source, destination)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	if len(translations) == 0 {
		return path, func() {}, nil
	}
	// Work on a disposable copy of the already validated v1 snapshot. The
	// destination write transaction stays locked throughout identity resolution.
	file, err := os.CreateTemp(scratch, "mapped-*.sqlite")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	input, err := os.Open(path)
	if err == nil {
		_, err = io.Copy(file, input)
		err = errors.Join(err, input.Close())
	}
	err = errors.Join(err, file.Close())
	if err == nil {
		err = translateArchive(ctx, file.Name(), translations)
	}
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("translate backup relationships: %w", err)
	}
	return file.Name(), cleanup, nil
}

func translateArchive(ctx context.Context, path string, pairs []profileIDPair) (err error) {
	db, err := openBackupDatabase(path, "rw")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// The input schema was compared to the application's own migrations.
	// Projections and deletion triggers must never run for a translation.
	rows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='trigger'")
	if err != nil {
		return err
	}
	var triggers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		triggers = append(triggers, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, name := range triggers {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+quoteBackupName(name)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE id_translation(source TEXT PRIMARY KEY,dest TEXT UNIQUE NOT NULL) WITHOUT ROWID"); err != nil {
		return err
	}
	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO id_translation VALUES(?,?)", p.source, p.destination); err != nil {
			return err
		}
	}
	if err := translateBackupTables(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func quoteBackupName(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Only these tables are inputs to the merge. Local search and timeline
// projections are rebuilt on the destination. Each field is identified by its
// role, so an equal string inside a message body or remote reference is opaque.
func translateBackupTables(ctx context.Context, tx *sql.Tx) error {
	const tables = `accounts revisions conversations conversation_revisions messages message_revisions
	operations archive_aliases legacy_room_archive_uids outbox message_references message_replies message_sender_identities
	message_retractions room_message_moderation message_edit_commands message_correction_facts
	message_content_heads message_canonical_aliases room_archive_echo_links disputed_room_archive_echoes expired_message_ids expired_message_references
	discarded_message_references staged_attachments account_communication_preferences contacts contact_revisions
	avatars avatar_revisions conversation_read_revisions conversation_read_states message_reads message_previews
	preview_revisions shared_content shared_content_state message_stars message_star_revisions accepted_direct_history
	recovered_sends discarded_recovered_sends recovered_send_operations recovered_send_revisions
	deleted_account_ids retired_recovered_sends restored_own_echoes legacy_image_claims active_legacy_image_claims`
	for _, table := range strings.Fields(tables) {
		rows, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return err
		}
		var columns []string
		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err != nil {
				_ = rows.Close()
				return err
			}
			columns = append(columns, col)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, col := range columns {
			switch col {
			case "id":
				if table != "messages" && table != "accounts" && table != "conversations" && table != "expired_message_ids" && table != "deleted_account_ids" {
					continue
				}
			case "entity_id":
				if table != "revisions" && table != "message_revisions" && table != "conversation_revisions" {
					continue
				}
			case "source_id":
				if table != "message_content_heads" {
					continue
				}
			case "echo_id", "original_id":
				if table != "room_archive_echo_links" {
					continue
				}
			case "message_id", "conversation_id", "account_id", "canonical_id", "alias_id", "peer_id", "resource_id":
			case "payload", "result":
				if err := translateBackupPayload(ctx, tx, table, col); err != nil {
					return err
				}
				continue
			case "message_ids":
				_, err := tx.ExecContext(ctx, `UPDATE conversation_read_revisions SET message_ids=(
SELECT json_group_array(COALESCE(t.dest,j.value) ORDER BY j.key) FROM json_each(message_ids) j LEFT JOIN id_translation t ON t.source=j.value)
WHERE EXISTS(SELECT 1 FROM json_each(message_ids) j JOIN id_translation t ON t.source=j.value)`)
				if err != nil {
					return err
				}
				continue
			default:
				continue
			}
			column := quoteBackupName(col)
			query := "UPDATE " + quoteBackupName(table) + " SET " + column + "=t.dest FROM id_translation t WHERE " + column + "=t.source"
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("translate %s.%s: %w", table, col, err)
			}
		}
	}
	return nil
}

func translateBackupPayload(ctx context.Context, tx *sql.Tx, table, column string) error {
	// These paths name local IDs in Oiê's persisted envelopes. Reply reference
	// IDs and OwnOriginID are on-wire values and must never be rewritten.
	paths := []string{"$.AccountID", "$.ConversationID", "$.MessageID", "$.PeerID", "$.Sender.ID", "$.FirstUnreadMessageID",
		"$.Composition.ReplyTo", "$.OutboundReply.TargetID", "$.Command.AccountID", "$.Command.ConversationID",
		"$.Command.MessageID", "$.Reply.TargetID"}
	switch table {
	case "messages", "message_revisions", "revisions", "conversations", "conversation_revisions", "message_content_heads",
		"recovered_sends", "recovered_send_revisions", "operations", "recovered_send_operations":
		paths = append(paths, "$.ID")
	}
	for _, path := range paths {
		query := "UPDATE " + quoteBackupName(table) + " SET " + column + "=json_replace(" + column + ",?1,t.dest) FROM id_translation t WHERE json_extract(" + column + ",?1)=t.source"
		if _, err := tx.ExecContext(ctx, query, path); err != nil {
			return fmt.Errorf("translate %s %s: %w", table, path, err)
		}
	}
	return nil
}
