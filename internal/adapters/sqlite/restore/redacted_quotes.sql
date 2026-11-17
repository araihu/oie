-- The archive may only retain the already redacted body and empty fallback.
-- Remove the quote from every retained revision of that same wire body.
CREATE TEMP TABLE restore_redacted_quotes AS
SELECT m.id AS message_id,m.conversation_id,d.id AS revision_id,
       COALESCE(a.canonical_id,m.id) AS canonical_id,r.body_hash AS wire_hash,
       min(json_extract(b.payload,'$.OriginalText')) AS body
FROM backup.message_replies r
JOIN restore_messages x ON x.id=r.message_id AND x.is_new=0
JOIN messages m ON m.id=x.id
JOIN backup.message_revisions b ON b.entity_id=m.id
  AND json_extract(b.payload,'$.WireBodySHA256')=r.body_hash
JOIN message_revisions d ON d.entity_id=m.id
LEFT JOIN message_canonical_aliases a ON a.alias_id=m.id
WHERE json_array_length(json_extract(r.payload,'$.Fallback'))=0
  AND json_type(b.payload,'$.OriginalText')='text'
  AND COALESCE(json_extract(b.payload,'$.OutboundReply'),'null')='null'
  AND COALESCE(json_extract(d.payload,'$.WireBodySHA256'),'')=''
  AND COALESCE(json_extract(d.payload,'$.OutboundReply'),'null')='null'
  AND oie_source_hash(json_extract(d.payload,'$.OriginalText'))=r.body_hash
  AND EXISTS (
    SELECT 1 FROM expired_message_references e
    WHERE e.conversation_id=m.conversation_id AND e.remote_id=json_extract(r.payload,'$.Target.ID')
      AND (json_extract(r.payload,'$.Target.Scope')='' OR e.scope=json_extract(r.payload,'$.Target.Scope'))
      AND (json_extract(r.payload,'$.Target.Author')='' OR e.author=json_extract(r.payload,'$.Target.Author'))
    UNION ALL
    SELECT 1 FROM discarded_message_references e
    JOIN effective_retention c ON c.account_id=e.account_id AND c.address=e.address AND c.kind=e.kind
    WHERE c.conversation_id=m.conversation_id AND c.days>0 AND e.age_ms<CAST(@now_ms AS INTEGER)-86400000*c.days
      AND e.remote_id=json_extract(r.payload,'$.Target.ID')
      AND (json_extract(r.payload,'$.Target.Scope')='' OR e.scope=json_extract(r.payload,'$.Target.Scope'))
      AND (json_extract(r.payload,'$.Target.Author')='' OR e.author=json_extract(r.payload,'$.Target.Author'))
  )
  AND NOT EXISTS (
    SELECT 1 FROM message_references ref JOIN messages target ON target.id=ref.message_id
    WHERE target.conversation_id=m.conversation_id AND ref.remote_id=json_extract(r.payload,'$.Target.ID')
      AND (json_extract(r.payload,'$.Target.Scope')='' OR ref.scope=json_extract(r.payload,'$.Target.Scope'))
      AND (json_extract(r.payload,'$.Target.Author')='' OR ref.author=json_extract(r.payload,'$.Target.Author'))
  )
GROUP BY m.id,m.conversation_id,d.id,canonical_id,r.body_hash
HAVING count(DISTINCT json_extract(b.payload,'$.OriginalText'))=1;

UPDATE message_revisions AS d
SET payload=json_set(d.payload,'$.OriginalText',r.body,'$.WireBodySHA256',r.wire_hash,'$.Reply',json('null'))
FROM restore_redacted_quotes r WHERE d.id=r.revision_id;

UPDATE messages AS m
SET payload=json_set(m.payload,'$.OriginalText',r.body,'$.WireBodySHA256',r.wire_hash,'$.Reply',json('null'))
FROM restore_redacted_quotes r
WHERE m.id=r.message_id AND m.revision_id=r.revision_id
  AND oie_source_hash(json_extract(m.payload,'$.OriginalText'))=r.wire_hash;

UPDATE message_content_heads AS h
SET payload=json_set(h.payload,'$.OriginalText',r.body,'$.WireBodySHA256',r.wire_hash,'$.Reply',json('null'))
FROM restore_redacted_quotes r
WHERE h.source_id=r.message_id AND h.revision_id=r.revision_id
  AND oie_source_hash(json_extract(h.payload,'$.OriginalText'))=r.wire_hash;

UPDATE changes AS c
SET payload=json_set(c.payload,'$.OriginalText',r.body,'$.WireBodySHA256',r.wire_hash,'$.Reply',json('null'))
FROM restore_redacted_quotes r
WHERE c.kind IN ('message.created','message.updated')
  AND json_extract(c.payload,'$.ID') IN (r.message_id,r.canonical_id)
  AND json_extract(c.payload,'$.RevisionID')=r.revision_id
  AND oie_source_hash(json_extract(c.payload,'$.OriginalText'))=r.wire_hash;

UPDATE message_replies AS d SET payload=json_set(d.payload,'$.Fallback',json('[]'))
FROM restore_redacted_quotes r WHERE d.message_id=r.message_id AND d.body_hash=r.wire_hash;

DELETE FROM shared_content_state WHERE message_id IN (
  SELECT message_id FROM restore_redacted_quotes UNION SELECT canonical_id FROM restore_redacted_quotes
);
UPDATE restore_chats SET quotes_redacted=1
WHERE id IN (SELECT conversation_id FROM restore_redacted_quotes);
DROP TABLE restore_redacted_quotes;
