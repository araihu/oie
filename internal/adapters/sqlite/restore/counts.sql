SELECT (SELECT COUNT(*) FROM restore_accounts WHERE is_new=1),
       (SELECT COUNT(*) FROM restore_chats WHERE is_new=1),
       (SELECT COUNT(*) FROM restore_messages WHERE is_new=1 AND NOT EXISTS(
           SELECT 1 FROM backup.message_canonical_aliases a WHERE a.alias_id=restore_messages.id));
