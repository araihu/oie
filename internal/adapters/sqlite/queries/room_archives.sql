-- name: GetRoomArchive :one
SELECT mode,progress FROM room_archives WHERE conversation_id=?;

-- name: PutRoomArchiveMode :exec
INSERT INTO room_archives(conversation_id,mode) VALUES(?,?)
ON CONFLICT(conversation_id) DO UPDATE SET mode=excluded.mode;

-- name: PutRoomArchiveProgress :exec
INSERT INTO room_archives(conversation_id,progress) VALUES(?,?)
ON CONFLICT(conversation_id) DO UPDATE SET progress=excluded.progress;

-- name: CountRoomArchiveMessages :one
SELECT COUNT(*) FROM messages m
WHERE m.conversation_id=sqlc.arg(conversation_id)
AND m.created_at_ms>=sqlc.arg(from_ms) AND m.created_at_ms<=sqlc.arg(to_ms)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id);

-- name: ListRoomArchiveConversations :many
SELECT * FROM conversations WHERE account_id=? AND json_extract(payload,'$.Kind')='room'
AND json_extract(payload,'$.Request') IS NULL
ORDER BY activity_sequence DESC,id;
