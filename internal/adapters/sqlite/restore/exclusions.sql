-- Deduplication may suppress an independently archived echo while its
-- original remains. Keep protocol identifiers only for the same verified
-- wire body, and bind them to the retained revision's body hash.
CREATE TEMP TABLE restore_represented_echoes AS
WITH bodies AS (
  SELECT e.source_id,e.retained_id,d.account_id,d.conversation_id,a.address AS account_address,
         c.address,CAST(json_extract(c.payload,'$.Kind') AS TEXT) AS kind,
         json_extract(b.payload,'$.OriginalText') AS echo_body,
         COALESCE(NULLIF(json_extract(b.payload,'$.WireBodySHA256'),''),
                  oie_source_hash(json_extract(b.payload,'$.OriginalText'))) AS echo_hash,
         json_extract(d.payload,'$.OriginalText') AS retained_body,
         json_extract(d.payload,'$.WireBodySHA256') AS retained_wire_hash,
         json_extract(d.payload,'$.AttachmentWireText') AS attachment_wire,
         json_type(d.payload,'$.Attachments[0]') IS NOT NULL AS has_attachments,
         COALESCE(NULLIF(json_extract(d.payload,'$.WireBodySHA256'),''),
                  oie_source_hash(json_extract(d.payload,'$.OriginalText'))) AS retained_hash
  FROM restore_suppressed_echoes e JOIN backup.messages b ON b.id=e.source_id
  JOIN messages d ON d.id=e.retained_id
  JOIN conversations c ON c.id=d.conversation_id AND c.account_id=d.account_id
  JOIN accounts a ON a.id=d.account_id
  WHERE json_type(b.payload,'$.OriginalText')='text' AND json_type(d.payload,'$.OriginalText')='text'
)
SELECT source_id,retained_id,account_id,conversation_id,account_address,address,kind,echo_hash,retained_hash
FROM bodies e
WHERE (NOT has_attachments AND echo_hash=retained_hash)
   OR (COALESCE(attachment_wire,'')<>'' AND oie_source_hash(attachment_wire)=echo_hash)
   OR oie_attachment_wire_matches(retained_body,attachment_wire,retained_wire_hash,echo_body)=1
   OR EXISTS (
     -- A copy may retain its own upload URL even when another verified
     -- attempt with the same origin ID used a different URL.
     SELECT 1 FROM message_revisions r WHERE r.entity_id=e.retained_id
       AND json_extract(r.payload,'$.IsOwn')=1 AND json_extract(r.payload,'$.ProviderID')=e.retained_id
       AND json_extract(r.payload,'$.AccountID')=e.account_id AND json_extract(r.payload,'$.ConversationID')=e.conversation_id
       AND json_extract(r.payload,'$.SendState') IN ('submitted','failed')
       AND json_extract(r.payload,'$.Attachments[0]') IS NOT NULL
       AND COALESCE(json_extract(r.payload,'$.AttachmentWireText'),'')<>''
       AND (oie_source_hash(json_extract(r.payload,'$.AttachmentWireText'))=e.echo_hash
            OR oie_attachment_wire_matches(json_extract(r.payload,'$.OriginalText'),json_extract(r.payload,'$.AttachmentWireText'),
               json_extract(r.payload,'$.WireBodySHA256'),e.echo_body)=1)
   );

CREATE TEMP VIEW restore_represented_echo_references AS
SELECT e.source_id,e.retained_id,e.conversation_id,r.scope,r.remote_id,r.author,e.retained_hash,e.echo_hash
FROM restore_represented_echoes e JOIN backup.message_references r ON r.message_id=e.source_id
WHERE r.source_hash=e.echo_hash AND r.remote_id<>''
  AND ((e.kind='room' AND r.scope='room:'||e.address AND instr(r.author,e.address||'/')=1)
       OR (e.kind='direct' AND r.scope='sender:'||e.account_address AND r.author=e.account_address));

UPDATE restore_chats SET history_changed=1 WHERE id IN (
  SELECT e.conversation_id FROM restore_represented_echo_references e
  WHERE NOT EXISTS (
    SELECT 1 FROM message_references r WHERE r.message_id=e.retained_id AND r.scope=e.scope
      AND r.remote_id=e.remote_id AND r.author=e.author AND r.source_hash=e.retained_hash
  ) OR (e.echo_hash<>e.retained_hash AND EXISTS (
    SELECT 1 FROM message_references r WHERE r.message_id=e.retained_id AND r.scope=e.scope
      AND r.remote_id=e.remote_id AND r.author=e.author AND r.source_hash=e.echo_hash
  ))
);
DELETE FROM message_references WHERE (message_id,scope,remote_id,author,source_hash) IN (
  SELECT retained_id,scope,remote_id,author,echo_hash FROM restore_represented_echo_references WHERE echo_hash<>retained_hash
);
INSERT INTO message_references(message_id,scope,remote_id,author,source_hash)
SELECT retained_id,scope,remote_id,author,retained_hash FROM restore_represented_echo_references
WHERE TRUE ON CONFLICT DO NOTHING;

INSERT INTO archive_aliases(account_id,scope,archive_id,message_id)
SELECT a.account_id,a.scope,a.archive_id,e.retained_id
FROM restore_represented_echoes e JOIN backup.archive_aliases a ON a.message_id=e.source_id AND a.account_id=e.account_id
WHERE a.archive_id<>'' AND (a.scope=e.account_address OR (e.kind='room' AND a.scope=e.address))
ON CONFLICT DO NOTHING;

-- References of source messages excluded by the destination can be needed
-- to mask retained replies. Neither the original body nor a revision is copied.
INSERT INTO discarded_message_references(account_id,address,kind,scope,remote_id,author,age_ms)
SELECT m.account_id,c.address,CAST(json_extract(c.payload,'$.Kind') AS TEXT),r.scope,r.remote_id,r.author,
       COALESCE(m.submitted_at_ms,m.timeline_at_ms,m.created_at_ms)
FROM backup.messages m JOIN restore_chats t ON t.id=m.conversation_id
JOIN conversations c ON c.id=t.id
JOIN backup.message_references r ON r.message_id=m.id
LEFT JOIN restore_messages x ON x.id=m.id LEFT JOIN backup.outbox o ON o.message_id=m.id
WHERE x.id IS NULL AND r.scope<>'' AND m.state NOT IN ('queued','dispatching','unknown') AND
      (o.state IS NULL OR o.state NOT IN ('queued','dispatching','unknown'))
  AND NOT EXISTS (
    SELECT 1 FROM restore_represented_echo_references e WHERE e.source_id=m.id AND e.scope=r.scope
      AND e.remote_id=r.remote_id AND e.author=r.author
  )
ON CONFLICT DO NOTHING;

-- An original discarded during archive ingestion can be absent from the
-- snapshot. Keep its reference only while the destination still excludes it.
INSERT INTO discarded_message_references(account_id,address,kind,scope,remote_id,author,age_ms)
SELECT d.*
FROM backup.discarded_message_references d
JOIN effective_retention c ON c.account_id=d.account_id AND c.address=d.address AND c.kind=d.kind
JOIN restore_chats t ON t.id=c.conversation_id
WHERE c.days>0 AND d.age_ms<CAST(@now_ms AS INTEGER)-86400000*c.days
AND NOT EXISTS (
    SELECT 1 FROM message_references r JOIN messages m ON m.id=r.message_id
    WHERE m.conversation_id=c.conversation_id AND r.scope=d.scope
      AND r.remote_id=d.remote_id AND r.author=d.author
)
ON CONFLICT DO NOTHING;

INSERT INTO expired_message_references
SELECT e.* FROM backup.expired_message_references e JOIN restore_chats c ON c.id=e.conversation_id
WHERE TRUE ON CONFLICT DO NOTHING;
