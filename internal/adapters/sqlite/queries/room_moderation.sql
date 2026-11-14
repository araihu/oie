-- name: InsertRoomModeration :execrows
INSERT INTO room_message_moderation(account_id, room_address, stanza_id, moderated_at, reason)
VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING;

-- name: GetRoomMessageModeration :one
SELECT moderation.moderated_at, moderation.reason
FROM messages root
JOIN conversations c ON c.id=root.conversation_id
JOIN messages source ON source.conversation_id=root.conversation_id AND source.account_id=root.account_id
JOIN message_references r ON r.message_id=source.id AND r.scope='room:'||c.address
JOIN room_message_moderation moderation ON moderation.account_id=root.account_id AND moderation.room_address=c.address AND moderation.stanza_id=r.remote_id
WHERE root.id=sqlc.arg(message_id)
AND (source.id=root.id OR source.id IN (SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=root.id))
ORDER BY moderation.moderated_at,moderation.stanza_id LIMIT 1;

-- name: ModeratedRoomMessageSources :many
SELECT DISTINCT m.id FROM messages m
JOIN conversations c ON c.id=m.conversation_id
JOIN message_references r ON r.message_id=m.id
WHERE m.account_id=sqlc.arg(account_id) AND c.address=sqlc.arg(room_address)
AND r.scope='room:'||c.address AND r.remote_id=sqlc.arg(stanza_id);

-- name: RemoveModeratedSharedContent :exec
DELETE FROM shared_content WHERE message_id=?;
