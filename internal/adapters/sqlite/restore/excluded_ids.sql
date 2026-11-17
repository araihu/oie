SELECT m.id FROM backup.messages m
LEFT JOIN restore_messages x ON x.id=m.id
WHERE m.conversation_id=? AND x.id IS NULL
AND m.state NOT IN ('queued','dispatching','unknown')
AND NOT EXISTS (SELECT 1 FROM messages d WHERE d.id=m.id)
UNION
SELECT json_extract(r.payload,'$.OutboundReply.TargetID')
FROM message_revisions r JOIN messages m ON m.id=r.entity_id AND m.conversation_id=?1
JOIN expired_message_ids e ON e.id=json_extract(r.payload,'$.OutboundReply.TargetID');
