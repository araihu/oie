-- name: LatestOwnMessage :one
SELECT m.* FROM original_transmission_order attempt JOIN messages m ON m.id=attempt.message_id
WHERE attempt.conversation_id=? AND m.conversation_id=attempt.conversation_id AND m.is_own=1
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
ORDER BY attempt.ordinal DESC LIMIT 1;

-- name: RecordOriginalTransmission :exec
INSERT INTO original_transmission_order(message_id,conversation_id)
SELECT m.id,m.conversation_id FROM messages m WHERE m.id=? AND m.is_own=1
AND EXISTS(SELECT 1 FROM operations op WHERE op.kind='send_message' AND op.resource_id=m.id);

-- name: OriginalSendReceipt :one
SELECT * FROM operations WHERE resource_id=? AND kind='send_message' LIMIT 1;

-- name: GetMessageEditCommand :one
SELECT * FROM message_edit_commands WHERE id=?;

-- name: GetActiveMessageEdit :one
SELECT * FROM message_edit_commands WHERE message_id=? AND state IN ('queued','dispatching','unknown') LIMIT 1;

-- name: InsertMessageEditCommand :exec
INSERT INTO message_edit_commands(id,revision_id,message_id,account_id,conversation_id,state,payload,next_attempt_at) VALUES(?,?,?,?,?,?,?,?);

-- name: UpdateMessageEditCommand :exec
UPDATE message_edit_commands SET revision_id=?,state=?,payload=?,next_attempt_at=?,attempt_count=attempt_count+? WHERE id=?;

-- name: PendingMessageEditCommands :many
SELECT * FROM message_edit_commands WHERE state='queued' AND next_attempt_at<=? ORDER BY next_attempt_at,id LIMIT 32;

-- name: InterruptedMessageEditCommands :many
SELECT * FROM message_edit_commands WHERE state='dispatching' ORDER BY id;

-- name: FirstContentRevisionSource :one
SELECT r.entity_id FROM message_revisions r WHERE r.id=?
AND r.rowid=(SELECT MIN(first.rowid) FROM message_revisions first WHERE first.entity_id=r.entity_id);
