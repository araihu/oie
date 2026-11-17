-- The destination can keep the wire body or recover the quoted original later.
-- Restore numeric spans over the verified local wire body as soon as the
-- matching archive supplies the surviving answer.
CREATE TEMP TABLE restore_reply_fallbacks AS
SELECT d.message_id,d.body_hash,m.conversation_id,COALESCE(a.canonical_id,m.id) AS canonical_id,
       oie_restore_reply_fallback(json_extract(local.payload,'$.OriginalText'),min(json_extract(redacted.payload,'$.OriginalText'))) AS spans
FROM message_replies d
JOIN backup.message_replies b ON b.message_id=d.message_id AND b.body_hash=d.body_hash
JOIN restore_messages x ON x.id=d.message_id AND x.is_new=0
JOIN messages m ON m.id=x.id
JOIN message_revisions local ON local.entity_id=m.id
JOIN backup.message_revisions redacted ON redacted.entity_id=m.id AND json_extract(redacted.payload,'$.WireBodySHA256')=d.body_hash
LEFT JOIN message_canonical_aliases a ON a.alias_id=m.id
WHERE COALESCE(json_array_length(json_extract(d.payload,'$.Fallback')),0)=0
  AND COALESCE(json_array_length(json_extract(b.payload,'$.Fallback')),0)=0
  AND json_extract(d.payload,'$.Target.ID')=json_extract(b.payload,'$.Target.ID')
  AND json_extract(d.payload,'$.Target.Scope') IS json_extract(b.payload,'$.Target.Scope')
  AND json_extract(d.payload,'$.Target.Author') IS json_extract(b.payload,'$.Target.Author')
  AND COALESCE(json_type(redacted.payload,'$.OutboundReply'),'null')='null'
  AND COALESCE(json_type(local.payload,'$.OutboundReply'),'null')='null'
  AND COALESCE(json_extract(local.payload,'$.WireBodySHA256'),'')=''
  AND oie_source_hash(json_extract(local.payload,'$.OriginalText'))=d.body_hash
GROUP BY d.message_id,d.body_hash,m.conversation_id,canonical_id,json_extract(local.payload,'$.OriginalText')
HAVING count(DISTINCT json_extract(redacted.payload,'$.OriginalText'))=1 AND spans IS NOT NULL;

UPDATE message_replies AS d SET payload=json_set(d.payload,'$.Fallback',json(f.spans))
FROM restore_reply_fallbacks f WHERE d.message_id=f.message_id AND d.body_hash=f.body_hash;

DELETE FROM shared_content_state WHERE message_id IN (
    SELECT message_id FROM restore_reply_fallbacks UNION SELECT canonical_id FROM restore_reply_fallbacks
);
UPDATE restore_chats SET history_changed=1 WHERE id IN (SELECT conversation_id FROM restore_reply_fallbacks);
DROP TABLE restore_reply_fallbacks;
