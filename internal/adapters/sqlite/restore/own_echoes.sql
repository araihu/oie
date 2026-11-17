INSERT INTO restored_own_echoes
SELECT e.account_id,e.conversation_id,e.message_id FROM backup.restored_own_echoes e
JOIN restore_chats x ON x.id=e.conversation_id
JOIN conversations c ON c.id=x.id AND c.account_id=e.account_id
WHERE TRUE ON CONFLICT DO NOTHING;
