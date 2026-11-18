-- An authenticated archive replay can establish the author of an existing
-- historical room row. Local outgoing messages have separate ownership.
SELECT b.id FROM backup.messages b
JOIN restore_messages x ON x.id=b.id AND x.is_new=0
JOIN messages d ON d.id=b.id AND d.account_id=b.account_id AND d.conversation_id=b.conversation_id
JOIN conversations c ON c.id=d.conversation_id AND c.account_id=d.account_id
WHERE json_extract(c.payload,'$.Kind')='room'
  AND b.is_own=1 AND b.state='submitted' AND json_extract(b.payload,'$.Historical')=1
  AND json_extract(b.payload,'$.Sender.ID')=b.account_id
  AND d.is_own=0 AND d.state='received' AND json_extract(d.payload,'$.Historical')=1
  AND NOT EXISTS (SELECT 1 FROM outbox o WHERE o.message_id=d.id)
  AND EXISTS (
      SELECT 1 FROM backup.archive_aliases alias
      WHERE alias.account_id=b.account_id AND alias.message_id=b.id AND alias.scope<>'' AND alias.archive_id<>''
  );
