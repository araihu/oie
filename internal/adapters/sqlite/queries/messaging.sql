-- name: GetConversation :one
SELECT * FROM conversations WHERE id=?;
-- name: FindConversation :one
SELECT * FROM conversations WHERE account_id=? AND address=?;
-- name: InsertConversation :exec
INSERT INTO conversations(id,revision_id,account_id,address,peer_id,payload,activity_sequence) VALUES(?,?,?,?,?,?,?);
-- name: InsertConversationRevision :exec
INSERT INTO conversation_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?);
-- name: UpdateConversation :exec
UPDATE conversations SET revision_id=?,payload=?,activity_sequence=?,unread_count=? WHERE id=?;
-- name: ListConversations :many
SELECT * FROM conversations
WHERE activity_sequence < sqlc.arg(cursor)
AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR account_id=CAST(sqlc.arg(account_id) AS TEXT))
AND archived=sqlc.arg(archived)
AND (CAST(sqlc.arg(favorites_only) AS INTEGER)=0 OR favorite=1)
AND (CAST(sqlc.arg(unread_only) AS INTEGER)=0 OR unread_count>0)
AND (CAST(sqlc.arg(kind) AS TEXT)='' OR json_extract(payload,'$.Kind')=CAST(sqlc.arg(kind) AS TEXT))
AND (CAST(sqlc.arg(query) AS TEXT)='' OR instr(lower(address || ' ' || json_extract(payload,'$.Title') || ' ' || json_extract(payload,'$.LastMessage')), lower(CAST(sqlc.arg(query) AS TEXT)))>0)
ORDER BY activity_sequence DESC LIMIT sqlc.arg(page_limit);
-- name: InsertMessage :exec
INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload) VALUES(?,?,?,?,?,?,?,?);
-- name: InsertMessageRevision :exec
INSERT INTO message_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?);
-- name: GetMessage :one
SELECT * FROM messages WHERE id=?;
-- name: FindProviderMessage :one
SELECT * FROM messages WHERE account_id=? AND conversation_id=? AND provider_id=? AND is_own=?;
-- name: ListMessages :many
SELECT * FROM messages WHERE conversation_id=sqlc.arg(conversation_id) AND (CAST(sqlc.arg(cursor) AS TEXT)='' OR id<CAST(sqlc.arg(cursor) AS TEXT)) ORDER BY id DESC LIMIT sqlc.arg(page_limit);
-- name: UpdateMessage :exec
UPDATE messages SET revision_id=?,state=?,payload=? WHERE id=?;
-- name: InsertOutbox :exec
INSERT INTO outbox(message_id,state,next_attempt_at) VALUES(?,'queued',?);
-- name: UpdateOutbox :exec
UPDATE outbox SET state=sqlc.arg(state),next_attempt_at=sqlc.arg(next_attempt_at),attempt_count=attempt_count+sqlc.arg(attempt_increment) WHERE message_id=sqlc.arg(message_id);
-- name: PendingMessages :many
SELECT m.* FROM outbox o JOIN messages m ON o.message_id=m.id WHERE o.state='queued' AND o.next_attempt_at<=? ORDER BY o.next_attempt_at,m.id LIMIT 100;
-- name: InterruptedMessages :many
SELECT * FROM messages WHERE state='dispatching';
-- name: AcquireWriteLock :exec
UPDATE engine_identity SET singleton=1 WHERE singleton=1;
