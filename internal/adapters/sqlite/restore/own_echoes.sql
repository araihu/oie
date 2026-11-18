INSERT INTO restored_own_echoes
SELECT e.account_id,e.conversation_id,e.message_id FROM backup.restored_own_echoes e
JOIN restore_chats x ON x.id=e.conversation_id
JOIN conversations c ON c.id=x.id AND c.account_id=e.account_id
WHERE NOT EXISTS (
  SELECT 1 FROM restore_legacy_direct_origins old
  WHERE old.is_backup=1 AND old.account_id=e.account_id AND old.conversation_id=e.conversation_id
    AND old.provider_id=e.message_id
) OR EXISTS (
  SELECT 1 FROM restore_source_echoes known
  WHERE known.account_id=e.account_id AND known.conversation_id=e.conversation_id AND known.wire_id=e.message_id
)
ON CONFLICT DO NOTHING;
