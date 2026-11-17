INSERT INTO operations
SELECT o.id,o.fingerprint,o.resource_id,'null',o.created_at,o.kind
FROM backup.operations o JOIN recovered_sends s ON s.message_id=o.resource_id
JOIN restore_chats c ON c.id=s.conversation_id
WHERE o.kind IN ('send_message','forward_message','update_pending_message','send_scheduled_message_now')
  AND (o.result<>'null' OR EXISTS(SELECT 1 FROM archive_held_operations h WHERE h.id=o.id))
ON CONFLICT(id) DO NOTHING;

INSERT INTO recovered_send_operations
SELECT d.id,d.resource_id,COALESCE(h.result,o.result)
FROM operations d JOIN backup.operations o ON o.id=d.id AND o.fingerprint=d.fingerprint
AND o.resource_id=d.resource_id AND o.kind=d.kind
JOIN recovered_sends s ON s.message_id=d.resource_id
JOIN restore_chats c ON c.id=s.conversation_id
LEFT JOIN archive_held_operations h ON h.id=o.id
WHERE d.result='null' AND d.kind IN ('send_message','forward_message','update_pending_message','send_scheduled_message_now')
  AND COALESCE(h.result,o.result)<>'null'
ON CONFLICT(operation_id) DO NOTHING;

-- A newer archive might contain evidence of successful transmission. Keep the
-- original receipt usable once the logical message is present in history.
UPDATE operations SET result=(SELECT b.result FROM backup.operations b WHERE b.id=operations.id)
WHERE result='null' AND EXISTS (
  SELECT 1 FROM backup.operations b JOIN restore_messages m ON m.id=b.resource_id AND m.is_new=1
  WHERE b.id=operations.id AND b.kind=operations.kind AND b.fingerprint=operations.fingerprint
    AND b.resource_id=operations.resource_id AND b.result<>'null'
    AND NOT EXISTS(SELECT 1 FROM discarded_recovered_sends x WHERE x.message_id=b.resource_id)
);

-- A cancellation from the archive can retire an already-present queue row.
-- Keep its actual acceptance receipt even when no new message was inserted.
INSERT INTO operations
SELECT o.* FROM backup.operations o JOIN restore_send_evidence e ON e.id=o.resource_id
JOIN messages m ON m.id=e.id AND m.state='cancelled'
WHERE e.state='cancelled' AND o.kind='cancel_message' AND o.result<>'null'
ON CONFLICT(id) DO NOTHING;
