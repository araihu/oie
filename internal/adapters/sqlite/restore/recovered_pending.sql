INSERT INTO recovered_sends
SELECT s.* FROM backup.recovered_sends s JOIN restore_chats c ON c.id=s.conversation_id
WHERE NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=s.message_id)
  AND NOT EXISTS(SELECT 1 FROM expired_message_ids m WHERE m.id=s.message_id)
  AND NOT EXISTS(SELECT 1 FROM discarded_recovered_sends m WHERE m.message_id=s.message_id)
  AND NOT EXISTS(SELECT 1 FROM operations m WHERE m.resource_id=s.message_id)
ON CONFLICT(message_id) DO NOTHING;
