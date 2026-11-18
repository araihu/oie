INSERT INTO expired_message_ids
SELECT e.id FROM backup.expired_message_ids e
WHERE NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=e.id)
ON CONFLICT(id) DO NOTHING;

INSERT INTO operations
SELECT o.* FROM backup.operations o
WHERE o.result='null' AND NOT EXISTS(SELECT 1 FROM archive_held_operations h WHERE h.id=o.id)
  AND (o.kind='register_account' AND NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=o.resource_id)
       OR o.kind IN ('send_message','cancel_message','update_pending_message','send_scheduled_message_now','forward_message')
          AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=o.resource_id)
       OR o.kind='edit_message' AND o.id=o.resource_id
          AND NOT EXISTS(SELECT 1 FROM message_edit_commands e WHERE e.id=o.resource_id))
ON CONFLICT(id) DO NOTHING;

INSERT INTO xmpp_message_exclusions
SELECT * FROM backup.xmpp_message_exclusions WHERE TRUE ON CONFLICT DO NOTHING;

CREATE TEMP TABLE restore_xmpp_excluded(message_id TEXT PRIMARY KEY) WITHOUT ROWID;
INSERT OR IGNORE INTO restore_xmpp_excluded
SELECT k.message_id FROM backup.xmpp_message_keys k JOIN xmpp_message_exclusions e
USING(account,chat,kind,key_type,authority,remote_id);

-- Once one key identifies deleted content, keep the other co-observed keys
-- without its body, so a later MAM-only or SID-only backup stays excluded.
INSERT INTO xmpp_message_exclusions
SELECT DISTINCT k.account,k.chat,k.kind,k.key_type,k.authority,k.remote_id
FROM backup.xmpp_message_keys k
WHERE k.message_id IN (SELECT message_id FROM restore_xmpp_excluded) ON CONFLICT DO NOTHING;

INSERT OR IGNORE INTO xmpp_unidentified_removals SELECT * FROM backup.xmpp_unidentified_removals;
