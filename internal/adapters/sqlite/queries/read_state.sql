-- name: GetConversationReadHead :one
SELECT revision_id,updated_at FROM conversation_read_states WHERE conversation_id=?;
-- name: InsertMessageRead :execrows
INSERT OR IGNORE INTO message_reads(message_id,revision_id) VALUES(?,?);
-- name: InsertConversationMessageReads :many
-- Use the same eligibility and canonical read evidence as ConversationUnreadSummary.
INSERT INTO message_reads(message_id,revision_id)
SELECT m.id,sqlc.arg(revision_id) FROM messages m
WHERE m.conversation_id=sqlc.arg(conversation_id) AND m.is_own=0 AND COALESCE(json_extract(m.payload,'$.Historical'),0)=0
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_correction_facts f WHERE f.message_id=m.id AND f.target_id<>'')
AND NOT EXISTS(SELECT 1 FROM message_reads r WHERE r.message_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a JOIN message_reads r ON r.message_id=a.alias_id
 WHERE a.canonical_id=m.id)
RETURNING message_id;
-- name: InsertReadRevision :exec
INSERT INTO conversation_read_revisions(id,conversation_id,parents,payload,message_ids,created_at) VALUES(?,?,?,?,?,?);
-- name: PutConversationReadHead :exec
INSERT INTO conversation_read_states(conversation_id,revision_id,updated_at) VALUES(?,?,?)
ON CONFLICT(conversation_id) DO UPDATE SET revision_id=excluded.revision_id,updated_at=excluded.updated_at;
-- name: ConversationUnreadSummary :one
SELECT COUNT(*) AS unread_count,
 CAST(COALESCE(MAX(json_extract(COALESCE(h.payload,m.payload),'$.MentionsYou')),0) AS INTEGER) AS unread_mention
FROM messages m
LEFT JOIN message_content_heads h ON h.canonical_id=m.id
WHERE m.conversation_id=? AND m.is_own=0 AND COALESCE(json_extract(m.payload,'$.Historical'),0)=0
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_correction_facts f WHERE f.message_id=m.id AND f.target_id<>'')
AND NOT EXISTS(SELECT 1 FROM message_reads r WHERE r.message_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a JOIN message_reads r ON r.message_id=a.alias_id
 WHERE a.canonical_id=m.id);
-- name: HasMessageRead :one
SELECT EXISTS(
 SELECT 1 FROM message_reads r WHERE r.message_id=sqlc.arg(canonical_id)
 UNION ALL
 SELECT 1 FROM message_canonical_aliases a JOIN message_reads r ON r.message_id=a.alias_id
 WHERE a.canonical_id=sqlc.arg(canonical_id)
);
