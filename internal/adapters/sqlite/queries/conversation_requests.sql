-- name: PendingDirectHistoryChecks :many
SELECT * FROM conversations
WHERE account_id = ? AND json_type(payload, '$.DirectHistoryCheck') = 'object'
ORDER BY activity_sequence LIMIT 16;

-- name: InsertRoomInvitationCredential :exec
INSERT INTO room_invitation_credentials (conversation_id, password) VALUES (?, ?);

-- name: GetRoomInvitationCredential :one
SELECT password FROM room_invitation_credentials WHERE conversation_id = ?;

-- name: InsertVisibleMessageChange :one
INSERT INTO changes(payload, occurred_at, kind)
SELECT sqlc.arg(payload), sqlc.arg(occurred_at), sqlc.arg(kind)
WHERE EXISTS (SELECT 1 FROM conversations c WHERE c.id=sqlc.arg(conversation_id) AND json_extract(c.payload,'$.Request') IS NULL)
RETURNING sequence;
