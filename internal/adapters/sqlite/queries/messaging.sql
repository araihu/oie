-- name: GetConversation :one
SELECT * FROM conversations WHERE id=?;
-- name: FindConversation :one
SELECT * FROM conversations WHERE account_id=? AND address=?;
-- name: CanPromoteRoomPlaceholder :one
SELECT NOT EXISTS(SELECT 1 FROM messages WHERE messages.conversation_id=sqlc.arg(room_id))
   AND NOT EXISTS(SELECT 1 FROM staged_attachments WHERE staged_attachments.conversation_id=sqlc.arg(room_id));
-- name: InsertConversation :exec
INSERT INTO conversations(id,revision_id,account_id,address,peer_id,payload,activity_sequence) VALUES(?,?,?,?,?,?,?);
-- name: InsertConversationRevision :exec
INSERT INTO conversation_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?);
-- name: GetConversationOpenedAt :one
SELECT created_at FROM conversation_revisions WHERE entity_id=? AND parents='[]' ORDER BY created_at,id LIMIT 1;
-- name: UpdateConversation :exec
UPDATE conversations SET revision_id=?,payload=?,activity_sequence=?,unread_count=? WHERE id=?;
-- name: ListConversations :many
SELECT sqlc.embed(conversations), CAST(CASE
    WHEN CAST(sqlc.arg(include_latest_message_page_count) AS INTEGER) = 0
        OR json_extract(conversations.payload,'$.Request') IS NOT NULL THEN -1
    ELSE (SELECT COUNT(*) FROM (
        SELECT 1 FROM messages AS m WHERE m.conversation_id=conversations.id
        AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
        AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
        ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC
        LIMIT sqlc.arg(message_page_limit)
    )) END AS INTEGER) AS latest_message_page_count
FROM conversations
JOIN (
    SELECT c.id FROM conversations AS c
    WHERE c.activity_sequence < sqlc.arg(cursor)
    AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR c.account_id=CAST(sqlc.arg(account_id) AS TEXT))
    AND (CAST(sqlc.arg(account_ids) AS TEXT)='[]' OR c.account_id IN (SELECT value FROM json_each(CAST(sqlc.arg(account_ids) AS TEXT))))
    AND c.archived=sqlc.arg(archived)
    AND (CAST(sqlc.arg(favorites_only) AS INTEGER)=0 OR c.favorite=1)
    AND (CAST(sqlc.arg(unread_only) AS INTEGER)=0 OR c.unread_count>0)
    AND (CAST(sqlc.arg(kind) AS TEXT)='' OR json_extract(c.payload,'$.Kind')=CAST(sqlc.arg(kind) AS TEXT))
    AND (CAST(sqlc.arg(query) AS TEXT)='' OR instr(lower(c.address || ' ' || json_extract(c.payload,'$.Title') || ' ' || CASE WHEN json_extract(c.payload,'$.Request') IS NULL THEN json_extract(c.payload,'$.LastMessage') ELSE '' END), lower(CAST(sqlc.arg(query) AS TEXT)))>0)
    ORDER BY c.activity_sequence DESC LIMIT sqlc.arg(page_limit)
) AS conversation_page ON conversation_page.id=conversations.id
ORDER BY conversations.activity_sequence DESC;
-- name: InsertMessage :exec
INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload,created_at_ms) VALUES(?,?,?,?,?,?,?,?,?);
-- name: InsertMessageRevision :exec
INSERT INTO message_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?);
-- name: GetMessage :one
SELECT * FROM messages WHERE id=?;
-- name: GetVisibleMessage :one
SELECT m.* FROM messages AS m WHERE m.id=?
AND EXISTS (SELECT 1 FROM conversations visible WHERE visible.id=m.conversation_id AND json_extract(visible.payload,'$.Request') IS NULL);
-- name: GetTimelineMessage :one
SELECT m.* FROM messages AS m WHERE m.id=?
AND EXISTS (SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id);
-- name: FindProviderMessage :one
-- Match the partial identity index used for messages with provider IDs.
SELECT * FROM messages WHERE account_id=? AND conversation_id=? AND provider_id=? AND is_own=? AND provider_id<>'';
-- name: ListMessages :many
SELECT m.* FROM messages AS m WHERE m.conversation_id=sqlc.arg(page_conversation_id)
AND EXISTS (SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id) AND (
CAST(sqlc.arg(page_cursor) AS TEXT)='' OR
(COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)<(SELECT COALESCE(x.timeline_at_ms,x.created_at_ms),x.id FROM messages AS x WHERE x.id=CAST(sqlc.arg(page_cursor) AS TEXT) AND x.conversation_id=sqlc.arg(page_conversation_id))
) ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT sqlc.arg(page_limit);
-- name: UpdateMessage :exec
UPDATE messages SET revision_id=?,state=?,payload=? WHERE id=?;
-- name: SetMessageTimelineAt :exec
UPDATE messages SET timeline_at_ms=? WHERE id=?;
-- name: InsertOutbox :exec
INSERT INTO outbox(message_id,state,next_attempt_at,scheduled_at) VALUES(?,'queued',?,?);
-- name: UpdateOutbox :exec
UPDATE outbox SET state=sqlc.arg(state),next_attempt_at=sqlc.arg(next_attempt_at),scheduled_at=sqlc.arg(scheduled_at),attempt_count=attempt_count+sqlc.arg(attempt_increment),wire_stage=sqlc.arg(wire_stage) WHERE message_id=sqlc.arg(message_id);
-- name: PendingMessages :many
SELECT m.* FROM outbox o JOIN messages m ON o.message_id=m.id WHERE o.state='queued' AND o.next_attempt_at<=? ORDER BY o.next_attempt_at,m.id LIMIT 100;
-- name: InterruptedMessages :many
SELECT * FROM messages WHERE state='dispatching';
-- name: AcquireWriteLock :exec
UPDATE engine_identity SET singleton=1 WHERE singleton=1;

-- name: ReconcileMessageAuthor :exec
UPDATE messages SET revision_id=?,is_own=1,state='submitted',payload=? WHERE id=? AND is_own=0;

-- name: ListMessagesAfter :many
SELECT m.* FROM messages AS m
JOIN messages AS anchor ON anchor.id=sqlc.arg(cursor_id) AND anchor.conversation_id=sqlc.arg(conversation_id)
WHERE m.conversation_id=sqlc.arg(conversation_id)
AND (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)>(COALESCE(anchor.timeline_at_ms,anchor.created_at_ms),anchor.id)
AND EXISTS (SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) ASC,m.id ASC LIMIT sqlc.arg(page_limit);

-- name: UpdateConversationPreferences :execrows
UPDATE conversations
SET revision_id=sqlc.arg(revision_id),payload=sqlc.arg(payload),archived=sqlc.arg(archived),favorite=sqlc.arg(favorite)
WHERE id=sqlc.arg(id) AND account_id=sqlc.arg(account_id) AND revision_id=sqlc.arg(expected_revision_id);
