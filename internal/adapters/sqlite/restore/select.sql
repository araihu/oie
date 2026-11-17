CREATE TEMP TABLE restore_accounts (id TEXT PRIMARY KEY, is_new INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_accounts
SELECT a.id, NOT EXISTS(SELECT 1 FROM accounts d WHERE d.id=a.id) FROM backup.accounts a
WHERE NOT EXISTS (SELECT 1 FROM deleted_account_ids d WHERE d.id=a.id)
  AND NOT EXISTS (
  SELECT 1 FROM operations o
  WHERE o.resource_id=a.id AND o.kind='register_account' AND o.result='null'
);

CREATE TEMP TABLE restore_chats (
  id TEXT PRIMARY KEY, is_new INTEGER NOT NULL, history_changed INTEGER NOT NULL DEFAULT 0,
  promote_room INTEGER NOT NULL DEFAULT 0, quotes_redacted INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
INSERT INTO restore_chats(id,is_new)
SELECT c.id, NOT EXISTS(SELECT 1 FROM conversations d WHERE d.id=c.id)
FROM backup.conversations c JOIN restore_accounts a ON a.id=c.account_id;

-- Opening a room can promote an empty direct placeholder under the same ID.
-- Skip an ancestor of that room so its old direct scope cannot import data.
DELETE FROM restore_chats
WHERE is_new=0 AND EXISTS (
  SELECT 1 FROM backup.conversations b
  JOIN conversations d ON d.id=b.id AND d.account_id=b.account_id AND d.address=b.address
  JOIN backup.conversation_revisions original ON original.id=b.revision_id AND original.entity_id=b.id
  WHERE b.id=restore_chats.id AND b.revision_id<>d.revision_id
    AND json_extract(b.payload,'$.Kind')='direct' AND json_extract(d.payload,'$.Kind')='room'
    AND json_extract(original.payload,'$.Kind')='direct'
    AND NOT EXISTS(SELECT 1 FROM backup.messages m WHERE m.conversation_id=b.id)
    AND NOT EXISTS(SELECT 1 FROM backup.staged_attachments f WHERE f.conversation_id=b.id)
    AND EXISTS (
      WITH RECURSIVE ancestors(id) AS (
        SELECT d.revision_id
        UNION
        SELECT parent.value FROM ancestors a
        JOIN conversation_revisions r ON r.id=a.id AND r.entity_id=d.id
        JOIN json_each(r.parents) parent ON parent.type='text'
      )
      SELECT 1 FROM ancestors a
      JOIN conversation_revisions known ON known.id=a.id AND known.entity_id=d.id
      WHERE known.id=b.revision_id AND json_extract(known.payload,'$.Kind')='direct'
    )
);

-- A room revision can promote its original destination placeholder when
-- the destination has no direct history or send intent in this scope.
UPDATE restore_chats SET promote_room=1, history_changed=1
WHERE is_new=0 AND EXISTS (
  SELECT 1 FROM backup.conversations b
  JOIN conversations d ON d.id=b.id AND d.account_id=b.account_id AND d.address=b.address
  JOIN conversation_revisions original ON original.id=d.revision_id AND original.entity_id=d.id
  WHERE b.id=restore_chats.id AND b.revision_id<>d.revision_id
    AND json_extract(d.payload,'$.Kind')='direct' AND json_extract(b.payload,'$.Kind')='room'
    AND json_extract(original.payload,'$.Kind')='direct'
    AND COALESCE(json_extract(d.payload,'$.Request'),'')=''
    AND json_extract(d.payload,'$.DirectHistoryCheck') IS NULL
    AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM staged_attachments f WHERE f.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM recovered_sends s WHERE s.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM accepted_direct_history h WHERE h.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM expired_message_references e WHERE e.conversation_id=d.id)
    AND EXISTS (
      WITH RECURSIVE ancestors(id) AS (
        SELECT root.id FROM backup.conversation_revisions root
        WHERE root.id=b.revision_id AND root.entity_id=b.id AND json_extract(root.payload,'$.Kind')='room'
        UNION
        SELECT parent.value FROM ancestors a
        JOIN backup.conversation_revisions r ON r.id=a.id AND r.entity_id=b.id
        JOIN json_each(r.parents) parent ON parent.type='text'
      )
      SELECT 1 FROM ancestors a
      JOIN backup.conversation_revisions known ON known.id=a.id AND known.entity_id=b.id
      WHERE known.id=d.revision_id AND json_extract(known.payload,'$.Kind')='direct'
    )
);

CREATE TEMP TABLE restore_messages (id TEXT PRIMARY KEY, is_new INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_messages
SELECT m.id, d.id IS NULL FROM backup.messages m
JOIN restore_chats chat ON chat.id=m.conversation_id
JOIN backup.conversations c ON c.id=chat.id AND c.account_id=m.account_id
LEFT JOIN messages d ON d.id=m.id
LEFT JOIN backup.outbox o ON o.message_id=m.id
LEFT JOIN backup.message_canonical_aliases a ON a.alias_id=m.id
LEFT JOIN conversation_retention r ON r.conversation_id=m.conversation_id
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

-- Restore send evidence onto the original local row. An own archive echo
-- retained under another row ID would duplicate that message in the timeline.
CREATE TEMP VIEW restore_source_echoes AS
SELECT m.*,COALESCE(NULLIF(json_extract(m.payload,'$.OwnOriginID'),''), m.provider_id) AS wire_id
FROM backup.messages m JOIN restore_chats x ON x.id=m.conversation_id
JOIN backup.conversations c ON c.id=x.id AND c.account_id=m.account_id
WHERE m.is_own=1 AND m.state='submitted' AND m.id<>m.provider_id
  AND (json_extract(m.payload,'$.OwnOriginID')<>'' OR json_extract(c.payload,'$.Kind')='direct'
       OR json_extract(m.payload,'$.Historical')=0);

CREATE TEMP TABLE restore_suppressed_echoes (
  source_id TEXT PRIMARY KEY, retained_id TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO restore_suppressed_echoes
SELECT m.id,d.id FROM restore_source_echoes m
JOIN restore_messages x ON x.id=m.id AND x.is_new=1
JOIN messages d ON d.id=m.wire_id AND d.id=d.provider_id
  AND d.account_id=m.account_id AND d.conversation_id=m.conversation_id AND d.is_own=1;
DELETE FROM restore_messages WHERE id IN (SELECT source_id FROM restore_suppressed_echoes);

DELETE FROM restore_messages
WHERE is_new=1 AND EXISTS (
  SELECT 1 FROM backup.message_canonical_aliases a WHERE a.alias_id=restore_messages.id
  AND NOT EXISTS(SELECT 1 FROM restore_messages root WHERE root.id=a.canonical_id)
  AND NOT EXISTS(SELECT 1 FROM messages root WHERE root.id=a.canonical_id)
);

-- Supplemental protocol facts can change the display and shared-content index
-- without making a message revision. Compare the same keys as the insertions.
CREATE TEMP TABLE restore_message_metadata_changes (id TEXT PRIMARY KEY) WITHOUT ROWID;
INSERT INTO restore_message_metadata_changes
SELECT b.message_id FROM backup.message_references b
JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
WHERE NOT EXISTS (
  SELECT 1 FROM message_references d WHERE d.message_id=b.message_id AND d.scope=b.scope
    AND d.remote_id=b.remote_id AND d.author=b.author AND d.source_hash=b.source_hash
)
UNION
SELECT b.message_id FROM backup.message_replies b
JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
WHERE NOT EXISTS (
  SELECT 1 FROM message_replies d WHERE d.message_id=b.message_id AND d.body_hash=b.body_hash
)
UNION
SELECT b.message_id FROM backup.message_sender_identities b
JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
WHERE NOT EXISTS (
  SELECT 1 FROM message_sender_identities d WHERE d.message_id=b.message_id
    AND d.personal_address=b.personal_address AND d.source_hash=b.source_hash
);

-- Detect a history change before inserting any revision rows. Chats whose
-- newest item and unread count stay the same still need a refresh when
-- retained messages, edit history, or protocol facts are added.
UPDATE restore_chats SET history_changed=1
WHERE id IN (
  SELECT m.conversation_id FROM backup.messages m JOIN restore_messages r ON r.id=m.id AND r.is_new=1
  UNION
  SELECT m.conversation_id FROM messages m JOIN restore_message_metadata_changes r ON r.id=m.id
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

DELETE FROM shared_content_state WHERE message_id IN (
  SELECT id FROM restore_message_metadata_changes
  UNION
  SELECT a.canonical_id FROM message_canonical_aliases a
  JOIN restore_message_metadata_changes r ON r.id=a.alias_id
);
DROP TABLE restore_message_metadata_changes;
