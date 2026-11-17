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
