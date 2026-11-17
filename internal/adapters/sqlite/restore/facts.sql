SELECT f.message_id,f.scope,f.author,f.remote_id,f.target_id
FROM backup.message_correction_facts f JOIN restore_messages x ON x.id=f.message_id
WHERE NOT EXISTS (
  SELECT 1 FROM message_correction_facts d
  WHERE d.message_id=f.message_id AND d.scope=f.scope AND d.author=f.author
    AND d.remote_id=f.remote_id AND d.target_id=f.target_id AND d.source_hash=f.source_hash
);
