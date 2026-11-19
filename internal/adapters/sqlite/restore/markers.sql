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

INSERT OR IGNORE INTO distinct_room_exclusion_keys SELECT * FROM backup.distinct_room_exclusion_keys;

CREATE TEMP TABLE restore_xmpp_excluded(message_id TEXT PRIMARY KEY) WITHOUT ROWID;
INSERT OR IGNORE INTO restore_xmpp_excluded
SELECT k.message_id FROM backup.observed_xmpp_message_keys k JOIN xmpp_message_exclusions e
USING(account,chat,kind,key_type,authority,remote_id);

-- Once one key identifies deleted content, keep the other co-observed keys
-- without its body, so a later MAM-only or SID-only backup stays excluded.
INSERT INTO xmpp_message_exclusions
SELECT DISTINCT k.account,k.chat,k.kind,k.key_type,k.authority,k.remote_id
FROM backup.observed_xmpp_message_keys k
WHERE k.message_id IN (SELECT message_id FROM restore_xmpp_excluded) ON CONFLICT DO NOTHING;

INSERT OR IGNORE INTO distinct_room_exclusion_keys
SELECT account,chat,key_type,remote_id FROM backup.distinct_room_message_keys
WHERE message_id IN (SELECT message_id FROM restore_xmpp_excluded);

-- Older backups may have observed only the live stanza ID or only the
-- room's MAM 2 result ID. Check the equivalent deletion marker unless the
-- source explicitly recorded a different ID for that form of evidence.
INSERT OR IGNORE INTO restore_xmpp_excluded
SELECT k.message_id FROM backup.observed_xmpp_message_keys k
JOIN xmpp_message_exclusions e USING(account,chat,kind,authority,remote_id)
WHERE k.kind='room' AND k.authority=k.chat AND e.key_type<>k.key_type
AND NOT EXISTS (SELECT 1 FROM backup.distinct_room_message_keys other WHERE other.message_id=k.message_id)
AND NOT EXISTS (
 SELECT 1 FROM distinct_room_exclusion_keys d WHERE d.account=e.account AND d.chat=e.chat
 AND d.key_type=e.key_type AND d.remote_id=e.remote_id
);

-- Before portable markers were available, restore used the retained reply
-- reference alone. Keep that protection for older removals and direct chats.
INSERT OR IGNORE INTO restore_xmpp_excluded
SELECT ref.message_id FROM backup.message_references ref
JOIN backup.messages m ON m.id=ref.message_id
JOIN expired_message_references e ON e.conversation_id=m.conversation_id AND e.scope=ref.scope
  AND e.remote_id=ref.remote_id AND e.author=ref.author
JOIN backup.conversations chat ON chat.id=m.conversation_id JOIN backup.accounts owner ON owner.id=chat.account_id
WHERE NOT (owner.protocol='xmpp' AND ref.scope='room:'||chat.address AND (
 EXISTS (SELECT 1 FROM projected_room_expirations p WHERE p.account=owner.address AND p.room=chat.address
         AND p.uid=ref.remote_id AND p.author=ref.author AND p.age_ms=e.age_ms)
 OR (EXISTS (SELECT 1 FROM backup.archive_aliases a WHERE a.message_id=ref.message_id AND a.scope=chat.address AND a.archive_id=ref.remote_id)
     AND EXISTS (SELECT 1 FROM distinct_room_exclusion_keys d WHERE d.account=owner.address AND d.chat=chat.address
                 AND d.key_type='stanza-id' AND d.remote_id=ref.remote_id))
));

INSERT OR IGNORE INTO xmpp_unidentified_removals SELECT * FROM backup.xmpp_unidentified_removals;
