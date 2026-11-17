INSERT INTO recovered_sends(message_id,conversation_id,source_engine_id,revision_id,original_state,attempt_count,scheduled_at,payload,created_at_ms)
SELECT m.id,m.conversation_id,@source_engine,m.revision_id,o.state,o.attempt_count,o.scheduled_at,m.payload,m.created_at_ms
FROM backup.messages m JOIN backup.outbox o ON o.message_id=m.id
JOIN restore_chats c ON c.id=m.conversation_id
WHERE o.state IN ('queued','dispatching','unknown') AND m.is_own=1
AND NOT EXISTS (SELECT 1 FROM messages d WHERE d.id=m.id OR
    (d.account_id=m.account_id AND d.conversation_id=m.conversation_id AND d.is_own=1 AND d.provider_id=m.id))
AND NOT EXISTS (SELECT 1 FROM expired_message_ids d WHERE d.id=m.id)
AND NOT EXISTS (SELECT 1 FROM discarded_recovered_sends d WHERE d.message_id=m.id)
AND NOT EXISTS (SELECT 1 FROM operations d WHERE d.resource_id=m.id)
ON CONFLICT(message_id) DO NOTHING;
