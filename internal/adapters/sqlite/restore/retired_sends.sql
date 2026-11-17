INSERT INTO retired_recovered_sends
SELECT e.* FROM backup.retired_recovered_sends e
JOIN restore_chats x ON x.id=e.conversation_id
JOIN conversations c ON c.id=x.id AND c.account_id=e.account_id
WHERE TRUE ON CONFLICT DO NOTHING;
