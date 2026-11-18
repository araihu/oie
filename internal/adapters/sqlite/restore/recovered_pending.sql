INSERT INTO restore_pending_candidates
SELECT s.* FROM backup.recovered_sends s JOIN restore_chats c ON c.id=s.conversation_id
WHERE TRUE ON CONFLICT(message_id) DO NOTHING;
