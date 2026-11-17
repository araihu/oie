-- name: RemoveBackupRoomPasswords :exec
DELETE FROM room_invitation_credentials;

-- name: ListBackupMedia :many
SELECT 'avatars' AS subdir, CAST(hash || '.png' AS TEXT) AS filename, hash, -1 AS size
FROM (
    SELECT CAST(json_extract(payload, '$.ImageHash') AS TEXT) AS hash FROM avatars
) WHERE hash <> ''
UNION
SELECT 'images', hash, hash, size FROM media_files
UNION
SELECT 'downloads', hash, hash, size FROM device_downloads WHERE kind='file' AND hash <> ''
UNION
SELECT 'staged', id, sha256, size FROM staged_attachments
ORDER BY 1,2;

-- name: SummarizeBackup :one
SELECT
  (SELECT COUNT(*) FROM accounts) AS accounts,
  (SELECT COUNT(*) FROM conversations) AS conversations,
  (SELECT COUNT(*) FROM messages WHERE NOT EXISTS
    (SELECT 1 FROM message_canonical_aliases WHERE alias_id=messages.id)) AS messages,
  (SELECT COUNT(*) FROM outbox WHERE state IN ('queued','dispatching','unknown')) AS pending_sends,
  (SELECT COUNT(*) FROM room_invitation_credentials) AS room_passwords;

-- name: CountRecoveredSends :one
SELECT COUNT(*) FROM recovered_sends;

-- name: ListRecoveredSends :many
SELECT r.*, c.address, c.account_id FROM recovered_sends r
JOIN conversations c ON c.id = r.conversation_id
ORDER BY r.created_at_ms, r.message_id;

-- name: GetRecoveredSend :one
SELECT * FROM recovered_sends WHERE message_id = ?;

-- name: DeleteRecoveredSend :exec
DELETE FROM recovered_sends WHERE message_id = ?;

-- name: DiscardRecoveredSend :exec
INSERT INTO discarded_recovered_sends(message_id) VALUES (?) ON CONFLICT DO NOTHING;

-- name: RemoveReconciledRecoveredSends :exec
DELETE FROM recovered_sends
WHERE EXISTS(SELECT 1 FROM messages m WHERE m.id=recovered_sends.message_id);

-- name: RecoveredSendHasDestinationEvidence :one
SELECT CAST(EXISTS(SELECT 1 FROM messages m WHERE m.id=sqlc.arg(message_id)
  OR (m.account_id=sqlc.arg(account_id) AND m.conversation_id=sqlc.arg(conversation_id)
      AND m.is_own=1 AND m.provider_id=sqlc.arg(message_id)))
OR EXISTS(SELECT 1 FROM operations o WHERE o.resource_id=sqlc.arg(message_id)
  AND NOT EXISTS(SELECT 1 FROM recovered_send_operations h WHERE h.operation_id=o.id))
OR EXISTS(SELECT 1 FROM expired_message_ids WHERE id=sqlc.arg(message_id)) AS INTEGER);

-- name: RestoreReviewedSendReceipts :exec
UPDATE operations SET result=(SELECT h.result FROM recovered_send_operations h WHERE h.operation_id=operations.id)
WHERE id IN (SELECT h.operation_id FROM recovered_send_operations h WHERE h.message_id=?);
