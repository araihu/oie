SELECT f.message_id,f.scope,f.author,f.remote_id,f.target_id
FROM backup.message_correction_facts f JOIN restore_messages x ON x.id=f.message_id
WHERE f.message_id NOT IN (SELECT source_id FROM restore_room_original_aliases)
AND NOT EXISTS (
  SELECT 1 FROM message_correction_facts d
  WHERE d.message_id=f.message_id AND d.scope=f.scope AND d.author=f.author
    AND d.remote_id=f.remote_id AND d.target_id=f.target_id AND d.source_hash=f.source_hash
)
UNION
SELECT witness.witness_id,witness.scope,witness.author,witness.remote_id,witness.target_id
FROM restore_local_edit_witnesses witness
JOIN backup.message_edit_commands imported ON imported.id=witness.remote_id
LEFT JOIN message_edit_commands local ON local.id=imported.id
LEFT JOIN message_canonical_aliases linked ON linked.alias_id=witness.witness_id
WHERE local.id IS NULL OR (local.state='queued' AND imported.state='submitted') OR linked.canonical_id IS NOT witness.target_id;
