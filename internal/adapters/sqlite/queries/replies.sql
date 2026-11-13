-- name: InsertMessageReference :execrows
INSERT INTO message_references(message_id,scope,remote_id,author,source_hash) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING;

-- name: InsertMessageReply :execrows
INSERT INTO message_replies(message_id,body_hash,payload) VALUES(?,?,?) ON CONFLICT DO NOTHING;

-- name: GetMessageReply :one
SELECT payload FROM message_replies WHERE message_id=? AND body_hash=?;

-- name: FindReplyTargets :many
-- Look up the remote reference before checking its conversation.
SELECT DISTINCT m.id,m.payload,r.source_hash
FROM message_references r CROSS JOIN messages m ON m.id=r.message_id
WHERE m.account_id=sqlc.arg(account_id) AND m.conversation_id=sqlc.arg(conversation_id)
AND r.remote_id=sqlc.arg(remote_id)
AND (CAST(sqlc.arg(scope) AS TEXT)='' OR r.scope=CAST(sqlc.arg(scope) AS TEXT))
AND (CAST(sqlc.arg(author) AS TEXT)='' OR r.author=CAST(sqlc.arg(author) AS TEXT))
LIMIT 129;

-- name: MessagesAtOrBeforeTarget :many
SELECT m.* FROM messages m WHERE m.conversation_id=sqlc.arg(conversation_id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=m.id)
-- Bound the leading expression so SQLite seeks into messages_timeline.
AND COALESCE(m.timeline_at_ms,m.created_at_ms)<=CAST(sqlc.arg(target_ms) AS INTEGER)
AND (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)<=(CAST(sqlc.arg(target_ms) AS INTEGER),CAST(sqlc.arg(target_id) AS TEXT))
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT sqlc.arg(page_limit);

-- name: MessagesAfterTarget :many
SELECT m.* FROM messages m WHERE m.conversation_id=sqlc.arg(conversation_id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=m.id)
-- Bound the leading expression so SQLite seeks into messages_timeline.
AND COALESCE(m.timeline_at_ms,m.created_at_ms)>=CAST(sqlc.arg(target_ms) AS INTEGER)
AND (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)>(CAST(sqlc.arg(target_ms) AS INTEGER),CAST(sqlc.arg(target_id) AS TEXT))
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms),m.id LIMIT sqlc.arg(page_limit);

-- name: GetMessageReferences :many
SELECT r.scope,r.remote_id,r.author FROM message_references r JOIN messages source ON source.id=r.message_id
WHERE r.message_id=sqlc.arg(message_id) AND r.source_hash=sqlc.arg(source_hash)
AND NOT EXISTS (
 SELECT 1 FROM message_references conflict JOIN messages other ON other.id=conflict.message_id
 WHERE other.account_id=source.account_id AND other.conversation_id=source.conversation_id
 AND conflict.scope=r.scope AND conflict.remote_id=r.remote_id AND conflict.message_id<>r.message_id
)
ORDER BY r.scope,r.remote_id,r.author LIMIT 8;
