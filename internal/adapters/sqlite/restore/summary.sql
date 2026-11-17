SELECT c.id, c.is_new, c.history_changed
FROM restore_chats c JOIN backup.conversations b ON b.id=c.id
ORDER BY c.is_new, b.activity_sequence, c.id;
