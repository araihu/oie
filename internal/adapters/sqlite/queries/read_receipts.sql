-- name: LatestDisplayedTarget :one
-- XEP-0333 signals a position, so an eligible target must precede any unread message.
SELECT m.id, COALESCE(m.timeline_at_ms,m.created_at_ms) AS position_ms,
       CAST(json_extract(m.payload,'$.ReadReceiptID') AS TEXT) AS remote_id
FROM messages m JOIN message_reads observed ON observed.message_id=m.id
WHERE observed.revision_id=sqlc.arg(read_revision_id)
AND m.conversation_id=sqlc.arg(conversation_id)
AND m.is_own=0 AND COALESCE(json_extract(m.payload,'$.Historical'),0)=0
AND json_extract(m.payload,'$.ReadReceiptID')<>''
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS(SELECT 1 FROM message_correction_facts f WHERE f.message_id=m.id AND f.target_id<>'')
AND NOT EXISTS(SELECT 1 FROM retained_message_retractions r WHERE r.source_id=m.id)
AND NOT EXISTS(SELECT 1 FROM shared_catalog_moderated_sources r WHERE r.message_id=m.id)
AND (CAST(sqlc.arg(first_unread_id) AS TEXT)='' OR
 (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)<
 (SELECT COALESCE(x.timeline_at_ms,x.created_at_ms),x.id FROM messages x WHERE x.id=CAST(sqlc.arg(first_unread_id) AS TEXT)))
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT 1;

-- name: AdvanceDisplayedHead :execrows
INSERT INTO conversation_displayed_heads(conversation_id,position_ms,message_id) VALUES(?,?,?)
ON CONFLICT(conversation_id) DO UPDATE SET position_ms=excluded.position_ms,message_id=excluded.message_id
WHERE (excluded.position_ms,excluded.message_id)>(conversation_displayed_heads.position_ms,conversation_displayed_heads.message_id);

-- name: IsCurrentDisplayedHead :one
SELECT EXISTS(SELECT 1 FROM conversation_displayed_heads h JOIN messages m ON m.id=h.message_id
WHERE h.conversation_id=? AND h.message_id=? AND m.is_own=0
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS(SELECT 1 FROM retained_message_retractions r WHERE r.source_id=m.id)
AND NOT EXISTS(SELECT 1 FROM shared_catalog_moderated_sources r WHERE r.message_id=m.id));
