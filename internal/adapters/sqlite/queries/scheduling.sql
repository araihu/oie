-- name: GetOutbox :one
SELECT * FROM outbox WHERE message_id = ?;

-- name: ListScheduledMessages :many
SELECT sqlc.embed(m), sqlc.embed(c)
FROM outbox AS o
JOIN messages AS m ON m.id = o.message_id
JOIN conversations AS c ON c.id = m.conversation_id
WHERE o.state = 'queued' AND o.scheduled_at <> ''
AND (CAST(sqlc.arg(conversation_ids) AS TEXT) = '[]' OR m.conversation_id IN (SELECT value FROM json_each(CAST(sqlc.arg(conversation_ids) AS TEXT))))
AND (o.scheduled_at, o.message_id) > (sqlc.arg(after_time), sqlc.arg(after_id))
ORDER BY o.scheduled_at, o.message_id LIMIT sqlc.arg(page_limit);

-- name: GetStagedAttachment :one
SELECT * FROM staged_attachments WHERE id = ?;

-- name: InsertStagedAttachment :exec
INSERT INTO staged_attachments (id, conversation_id, file_name, media_type, size, sha256)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeletePendingReplyMetadata :exec
DELETE FROM message_replies WHERE message_id = ?;
