-- name: InsertMessageReference :execrows
INSERT INTO message_references(message_id,scope,remote_id,author,source_hash) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING;

-- name: InsertMessageReply :execrows
INSERT INTO message_replies(message_id,body_hash,payload) VALUES(?,?,?) ON CONFLICT DO NOTHING;

-- name: GetMessageReply :one
SELECT payload FROM message_replies WHERE message_id=? AND body_hash=?;

-- name: FindReplyTargets :many
SELECT DISTINCT m.id,m.payload,r.source_hash FROM message_references r JOIN messages m ON m.id=r.message_id
WHERE m.account_id=sqlc.arg(account_id) AND m.conversation_id=sqlc.arg(conversation_id)
AND r.remote_id=sqlc.arg(remote_id)
AND (CAST(sqlc.arg(scope) AS TEXT)='' OR r.scope=CAST(sqlc.arg(scope) AS TEXT))
AND (CAST(sqlc.arg(author) AS TEXT)='' OR r.author=CAST(sqlc.arg(author) AS TEXT))
LIMIT 2;

-- name: MessagesAtOrBeforeTarget :many
SELECT m.* FROM messages m WHERE m.conversation_id=sqlc.arg(conversation_id)
AND (m.created_at_ms,m.id)<=(SELECT t.created_at_ms,t.id FROM messages t WHERE t.id=sqlc.arg(target_id) AND t.conversation_id=sqlc.arg(conversation_id))
ORDER BY m.created_at_ms DESC,m.id DESC LIMIT sqlc.arg(page_limit);

-- name: MessagesAfterTarget :many
SELECT m.* FROM messages m WHERE m.conversation_id=sqlc.arg(conversation_id)
AND (m.created_at_ms,m.id)>(SELECT t.created_at_ms,t.id FROM messages t WHERE t.id=sqlc.arg(target_id) AND t.conversation_id=sqlc.arg(conversation_id))
ORDER BY m.created_at_ms,m.id LIMIT sqlc.arg(page_limit);
