INSERT INTO expired_message_ids
SELECT e.id FROM backup.expired_message_ids e
WHERE NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=e.id)
ON CONFLICT(id) DO NOTHING;

INSERT INTO operations
SELECT o.* FROM backup.operations o
WHERE o.result='null' AND NOT EXISTS(SELECT 1 FROM archive_held_operations h WHERE h.id=o.id)
  AND (o.kind='register_account' AND NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=o.resource_id)
       OR o.kind IN ('send_message','cancel_message','update_pending_message','forward_message')
          AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=o.resource_id))
ON CONFLICT(id) DO NOTHING;
