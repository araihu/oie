-- References of source messages excluded by the destination can be needed
-- to mask retained replies. Neither the original body nor a revision is copied.
INSERT INTO expired_message_references(conversation_id,scope,remote_id,author,age_ms)
SELECT m.conversation_id,r.scope,r.remote_id,r.author,COALESCE(m.submitted_at_ms,m.timeline_at_ms,m.created_at_ms)
FROM backup.messages m JOIN restore_chats c ON c.id=m.conversation_id
JOIN backup.message_references r ON r.message_id=m.id
LEFT JOIN restore_messages x ON x.id=m.id LEFT JOIN backup.outbox o ON o.message_id=m.id
WHERE x.id IS NULL AND m.state NOT IN ('queued','dispatching','unknown') AND
      (o.state IS NULL OR o.state NOT IN ('queued','dispatching','unknown'))
ON CONFLICT DO NOTHING;

INSERT INTO expired_message_references
SELECT e.* FROM backup.expired_message_references e JOIN restore_chats c ON c.id=e.conversation_id
WHERE TRUE ON CONFLICT DO NOTHING;
