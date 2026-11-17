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
SELECT CAST(EXISTS(SELECT 1 FROM messages m JOIN conversations chat ON chat.id=m.conversation_id
  WHERE m.id=sqlc.arg(message_id)
  OR (m.account_id=sqlc.arg(account_id) AND m.conversation_id=sqlc.arg(conversation_id)
      AND m.is_own=1 AND (
        json_extract(m.payload,'$.OwnOriginID')=sqlc.arg(message_id)
        OR (m.provider_id=sqlc.arg(message_id)
            AND (json_extract(chat.payload,'$.Kind')='direct' OR json_extract(m.payload,'$.Historical')=0))
      )))
OR EXISTS(SELECT 1 FROM operations o WHERE o.resource_id=sqlc.arg(message_id)
  AND NOT EXISTS(SELECT 1 FROM recovered_send_operations h WHERE h.operation_id=o.id))
OR EXISTS(SELECT 1 FROM expired_message_ids WHERE id=sqlc.arg(message_id))
OR EXISTS(SELECT 1 FROM restored_own_echoes e WHERE e.account_id=sqlc.arg(account_id)
   AND e.conversation_id=sqlc.arg(conversation_id) AND e.message_id=sqlc.arg(message_id))
OR EXISTS(
 SELECT 1 FROM conversations c JOIN accounts a ON a.id=c.account_id
 JOIN message_retractions r ON r.account_id=c.account_id AND r.address=c.address AND r.kind='direct'
   AND r.scope='xmpp:correct:sender:'||a.address AND r.remote_id=sqlc.arg(message_id)
 WHERE c.id=sqlc.arg(conversation_id) AND json_extract(c.payload,'$.Kind')='direct'
) AS INTEGER);

-- name: RestoreReviewedSendReceipts :exec
UPDATE operations SET result=(SELECT h.result FROM recovered_send_operations h WHERE h.operation_id=operations.id)
WHERE id IN (SELECT h.operation_id FROM recovered_send_operations h WHERE h.message_id=?);

-- name: ListRecoveredSendRevisions :many
SELECT * FROM recovered_send_revisions WHERE message_id=? ORDER BY id;

-- name: PutRecoveredSendRevision :exec
INSERT INTO recovered_send_revisions (id,message_id,parents,payload,created_at) VALUES (?,?,?,?,?)
ON CONFLICT(id) DO NOTHING;

-- name: UpdateRecoveredSend :exec
UPDATE recovered_sends SET revision_id=?, original_state=?, attempt_count=?, scheduled_at=?, payload=? WHERE message_id=?;

-- name: PreserveResumedRevision :exec
INSERT INTO message_revisions (id,entity_id,parents,payload,created_at)
SELECT id,message_id,parents,payload,created_at FROM recovered_send_revisions WHERE message_id=?
ON CONFLICT(id) DO UPDATE SET parents=excluded.parents,payload=excluded.payload,created_at=excluded.created_at
WHERE message_revisions.entity_id=excluded.entity_id;

-- name: HasRecoveredSendReceipt :one
SELECT EXISTS(SELECT 1 FROM recovered_send_operations WHERE operation_id=?);

-- name: ScrubRecoveredSendPayloads :exec
UPDATE recovered_sends SET payload=oie_strip_expired_outbound_quote(payload)
WHERE conversation_id=sqlc.arg(owner_id)
  AND json_extract(payload,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ScrubRecoveredSendRevisions :exec
UPDATE recovered_send_revisions SET payload=oie_strip_expired_outbound_quote(payload)
WHERE message_id IN (SELECT message_id FROM recovered_sends WHERE conversation_id=sqlc.arg(owner_id))
  AND json_extract(payload,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ScrubRecoveredSendReceipts :exec
UPDATE recovered_send_operations SET result=oie_strip_expired_outbound_quote(result)
WHERE message_id IN (SELECT message_id FROM recovered_sends WHERE conversation_id=sqlc.arg(owner_id))
  AND json_extract(result,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: RememberOwnOrigin :exec
INSERT INTO restored_own_echoes (account_id,conversation_id,message_id)
SELECT c.account_id,c.id,sqlc.arg(message_id) FROM conversations c
WHERE c.account_id=sqlc.arg(account_id) AND c.address=sqlc.arg(address)
  AND json_extract(c.payload,'$.Kind')=sqlc.arg(kind)
ON CONFLICT DO NOTHING;
