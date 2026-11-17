CREATE TEMP TABLE restore_accounts (id TEXT PRIMARY KEY, is_new INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_accounts
SELECT a.id, NOT EXISTS(SELECT 1 FROM accounts d WHERE d.id=a.id) FROM backup.accounts a
WHERE NOT EXISTS (SELECT 1 FROM deleted_account_ids d WHERE d.id=a.id)
  AND NOT EXISTS (
  SELECT 1 FROM operations o
  WHERE o.resource_id=a.id AND o.kind='register_account' AND o.result='null'
);

CREATE TEMP TABLE restore_chats (id TEXT PRIMARY KEY, is_new INTEGER NOT NULL, history_changed INTEGER NOT NULL DEFAULT 0) WITHOUT ROWID;
INSERT INTO restore_chats(id,is_new)
SELECT c.id, NOT EXISTS(SELECT 1 FROM conversations d WHERE d.id=c.id)
FROM backup.conversations c JOIN restore_accounts a ON a.id=c.account_id;

CREATE TEMP TABLE restore_messages (id TEXT PRIMARY KEY, is_new INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_messages
SELECT m.id, d.id IS NULL FROM backup.messages m
JOIN restore_chats chat ON chat.id=m.conversation_id
JOIN backup.conversations c ON c.id=chat.id AND c.account_id=m.account_id
LEFT JOIN messages d ON d.id=m.id
LEFT JOIN backup.outbox o ON o.message_id=m.id
LEFT JOIN backup.message_canonical_aliases a ON a.alias_id=m.id
LEFT JOIN effective_retention r ON r.conversation_id=m.conversation_id
CROSS JOIN global_retention g
WHERE (d.id IS NOT NULL OR (
  -- Apply the destination's policy before inserting any new history. Aliases
  -- follow the root's age/star, and cannot revive an excluded root.
  NOT EXISTS(SELECT 1 FROM expired_message_ids e WHERE e.id IN (m.id,a.canonical_id))
  AND NOT EXISTS (
    SELECT 1 FROM expired_message_references e
    JOIN backup.message_references ref ON ref.scope=e.scope AND ref.remote_id=e.remote_id AND ref.author=e.author
    WHERE e.conversation_id=m.conversation_id AND ref.message_id IN (m.id,a.canonical_id)
  )
  AND (
    m.state='cancelled'
    OR COALESCE(r.days,CASE json_extract(c.payload,'$.Kind') WHEN 'room' THEN g.rooms_days ELSE g.direct_days END)=0
    OR COALESCE(
        (SELECT COALESCE(root.submitted_at_ms,root.timeline_at_ms,root.created_at_ms)
         FROM backup.messages root WHERE root.id=a.canonical_id),
        m.submitted_at_ms,m.timeline_at_ms,m.created_at_ms
    ) >= @now_ms - 86400000 * COALESCE(r.days,CASE json_extract(c.payload,'$.Kind') WHEN 'room' THEN g.rooms_days ELSE g.direct_days END)
    OR EXISTS (
      SELECT 1 FROM backup.message_stars s WHERE s.starred=1 AND (
        s.message_id=COALESCE(a.canonical_id,m.id)
        OR s.message_id IN (SELECT al.alias_id FROM backup.message_canonical_aliases al WHERE al.canonical_id=COALESCE(a.canonical_id,m.id))
      )
    )
  )
))
AND (o.state IS NULL OR o.state NOT IN ('queued','dispatching','unknown'))
AND m.state NOT IN ('queued','dispatching','unknown');

DELETE FROM restore_messages
WHERE is_new=1 AND EXISTS (
  SELECT 1 FROM backup.message_canonical_aliases a WHERE a.alias_id=restore_messages.id
  AND NOT EXISTS(SELECT 1 FROM restore_messages root WHERE root.id=a.canonical_id)
  AND NOT EXISTS(SELECT 1 FROM messages root WHERE root.id=a.canonical_id)
);

-- Detect a history change before inserting any revision rows. Chats whose
-- newest item and unread count stay the same still need a refresh when
-- retained messages or edit history are added.
UPDATE restore_chats SET history_changed=1
WHERE id IN (
  SELECT m.conversation_id FROM backup.messages m JOIN restore_messages r ON r.id=m.id AND r.is_new=1
  UNION
  SELECT m.conversation_id FROM backup.message_revisions b
  JOIN restore_messages r ON r.id=b.entity_id AND r.is_new=0
  JOIN messages m ON m.id=r.id
  WHERE NOT EXISTS(SELECT 1 FROM message_revisions d WHERE d.id=b.id)
  UNION
  SELECT m.conversation_id FROM backup.message_star_revisions b
  JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
  JOIN messages m ON m.id=r.id
  WHERE NOT EXISTS(SELECT 1 FROM message_star_revisions d WHERE d.id=b.id)
  UNION
  SELECT c.id FROM backup.message_retractions b
  JOIN conversations c ON c.account_id=b.account_id AND c.address=b.address
  WHERE NOT EXISTS(
    SELECT 1 FROM message_retractions d WHERE d.account_id=b.account_id
      AND d.kind=b.kind AND d.address=b.address AND d.scope=b.scope AND d.remote_id=b.remote_id
  )
  UNION
  SELECT c.id FROM backup.room_message_moderation b
  JOIN conversations c ON c.account_id=b.account_id AND c.address=b.room_address
  WHERE NOT EXISTS(
    SELECT 1 FROM room_message_moderation d
    WHERE d.account_id=b.account_id AND d.room_address=b.room_address AND d.stanza_id=b.stanza_id
  )
);
