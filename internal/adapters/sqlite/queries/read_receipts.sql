-- name: RecordDisplayedPolicyEpoch :exec
UPDATE conversation_read_revisions SET displayed_policy_epoch=sqlc.arg(epoch) WHERE id=sqlc.arg(read_revision_id);

-- name: LatestDisplayedTarget :one
-- XEP-0333 signals a position, so an eligible target must precede any unread message.
SELECT m.id, COALESCE(m.timeline_at_ms,m.created_at_ms) AS position_ms,
       CAST(json_extract(m.payload,'$.ReadReceiptID') AS TEXT) AS remote_id
FROM messages m JOIN message_reads observed ON observed.message_id=m.id
JOIN conversation_read_revisions observation ON observation.id=observed.revision_id
LEFT JOIN conversation_displayed_heads sent ON sent.conversation_id=m.conversation_id
WHERE observation.displayed_policy_epoch=sqlc.arg(policy_epoch)
AND (sent.message_id IS NULL OR (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)>(sent.position_ms,sent.message_id))
AND m.conversation_id=sqlc.arg(conversation_id)
AND COALESCE(m.timeline_at_ms,m.created_at_ms)>=COALESCE(
 (SELECT h.position_ms FROM conversation_displayed_heads h WHERE h.conversation_id=sqlc.arg(conversation_id)), -9223372036854775808)
AND COALESCE(m.timeline_at_ms,m.created_at_ms)<=COALESCE(
 (SELECT COALESCE(x.timeline_at_ms,x.created_at_ms) FROM messages x WHERE x.id=CAST(sqlc.arg(first_unread_id) AS TEXT)), 9223372036854775807)
AND m.is_own=0 AND COALESCE(json_extract(m.payload,'$.Historical'),0)=0
AND json_extract(m.payload,'$.ReadReceiptID')<>''
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_correction_facts f WHERE f.message_id=m.id AND f.target_id<>'')
AND NOT EXISTS(
 SELECT 1 FROM retained_message_retractions r
 WHERE r.source_id IN (SELECT m.id UNION ALL SELECT a.alias_id FROM message_canonical_aliases a WHERE a.canonical_id=m.id)
)
AND NOT EXISTS(
 SELECT 1 FROM message_references ref JOIN room_message_moderation moderation
 ON moderation.account_id=m.account_id AND ref.scope='room:'||moderation.room_address AND ref.remote_id=moderation.stanza_id
 WHERE ref.message_id IN (SELECT m.id UNION ALL SELECT a.alias_id FROM message_canonical_aliases a WHERE a.canonical_id=m.id)
)
AND (CAST(sqlc.arg(first_unread_id) AS TEXT)='' OR
 (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)<
 (SELECT COALESCE(x.timeline_at_ms,x.created_at_ms),x.id FROM messages x WHERE x.id=CAST(sqlc.arg(first_unread_id) AS TEXT)))
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT 1;

-- name: AdvanceDisplayedHead :execrows
INSERT INTO conversation_displayed_heads(conversation_id,position_ms,message_id) VALUES(?,?,?)
ON CONFLICT(conversation_id) DO UPDATE SET position_ms=excluded.position_ms,message_id=excluded.message_id
WHERE (excluded.position_ms,excluded.message_id)>(conversation_displayed_heads.position_ms,conversation_displayed_heads.message_id);

-- name: GetGlobalReadReceipts :one
SELECT CAST(revision AS TEXT) AS revision, enabled FROM engine_read_receipt_settings WHERE singleton=1;

-- name: PutGlobalReadReceipts :exec
UPDATE engine_read_receipt_settings SET enabled=?, revision=revision+1 WHERE singleton=1;

-- name: GetConversationReadReceipts :one
SELECT revision, policy_epoch, override_enabled, global_enabled, effective_enabled
FROM conversation_read_receipt_policy WHERE conversation_id=?;

-- name: PutConversationReadReceipts :exec
INSERT INTO conversation_read_receipt_settings(conversation_id,enabled,revision) VALUES(?,?,1)
ON CONFLICT(conversation_id) DO UPDATE SET enabled=excluded.enabled,revision=conversation_read_receipt_settings.revision+1;
