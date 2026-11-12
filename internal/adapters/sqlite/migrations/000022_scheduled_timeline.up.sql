ALTER TABLE messages ADD COLUMN timeline_at_ms INTEGER DEFAULT NULL;

-- Recover the release time of schedules that the previous schema already
-- dispatched or released with Send now. Use revision insertion order because
-- another release can follow a reschedule after the engine clock moves back.
WITH released AS (
    SELECT r.entity_id, MAX(r.rowid) AS release_row
    FROM message_revisions r
    WHERE EXISTS (
        SELECT 1 FROM json_each(r.parents) p
        JOIN message_revisions previous ON previous.id = p.value AND previous.entity_id = r.entity_id
        WHERE json_extract(previous.payload, '$.ScheduledAt') IS NOT NULL
          AND ((json_extract(r.payload, '$.ScheduledAt') IS NOT NULL
                AND json_extract(r.payload, '$.SendState') IN ('submitted', 'unknown', 'failed')
                AND json_extract(previous.payload, '$.SendState') IN ('queued', 'dispatching'))
               OR (json_extract(r.payload, '$.ScheduledAt') IS NULL
                   AND json_extract(r.payload, '$.SendState') = 'queued'))
    )
    GROUP BY r.entity_id
)
UPDATE messages SET timeline_at_ms = (
    SELECT unixepoch(substr(r.created_at, 1, 19) || 'Z') * 1000 + CAST(substr(r.created_at, 21, 3) AS INTEGER)
    FROM released JOIN message_revisions r ON r.rowid = released.release_row WHERE released.entity_id = messages.id
)
WHERE id IN (SELECT entity_id FROM released)
AND NOT EXISTS (
    SELECT 1 FROM outbox o WHERE o.message_id = messages.id
    AND o.scheduled_at <> '' AND o.state IN ('queued', 'dispatching', 'cancelled')
);

CREATE INDEX messages_timeline ON messages(conversation_id, COALESCE(timeline_at_ms, created_at_ms) DESC, id DESC);

CREATE VIEW hidden_scheduled_messages AS
SELECT o.message_id FROM outbox o JOIN messages m ON m.id = o.message_id
WHERE (o.scheduled_at <> '' AND m.timeline_at_ms IS NULL AND o.state IN ('queued', 'dispatching', 'cancelled'))
   OR (o.state = 'cancelled' AND m.timeline_at_ms IS NOT NULL);

-- The summary is a projection. Repair it without changing the conversation
-- revision, activity sequence, unread state, or immutable operation receipts.
WITH affected AS (
    SELECT DISTINCT m.conversation_id
    FROM messages m JOIN outbox o ON o.message_id = m.id
    WHERE m.timeline_at_ms IS NOT NULL
       OR (EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id = m.id)
           AND EXISTS (SELECT 1 FROM message_revisions r WHERE r.entity_id = m.id
                       AND json_extract(r.payload, '$.ScheduledAt') IS NULL))
), latest AS (
    SELECT conversation_id, (
        SELECT m.id FROM messages m WHERE m.conversation_id = affected.conversation_id
        AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id = m.id)
        AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id = m.id)
        ORDER BY COALESCE(m.timeline_at_ms, m.created_at_ms) DESC, m.id DESC LIMIT 1
    ) AS message_id FROM affected
)
UPDATE conversations SET payload = (
    SELECT json_set(conversations.payload,
        '$.LastMessage', COALESCE(json_extract(h.payload, '$.OriginalText'), json_extract(m.payload, '$.OriginalText'), ''),
        '$.UpdatedAt', COALESCE(
            strftime('%Y-%m-%dT%H:%M:%fZ', m.timeline_at_ms / 1000.0, 'unixepoch'),
            json_extract(h.payload, '$.CreatedAt'), json_extract(m.payload, '$.CreatedAt'),
            (SELECT json_extract(r.payload, '$.UpdatedAt') FROM conversation_revisions r
             WHERE r.entity_id = conversations.id AND r.parents = '[]' ORDER BY r.created_at, r.id LIMIT 1),
            json_extract(conversations.payload, '$.UpdatedAt')))
    FROM latest l LEFT JOIN messages m ON m.id = l.message_id
    LEFT JOIN message_content_heads h ON h.canonical_id = m.id
    WHERE l.conversation_id = conversations.id
)
WHERE id IN (SELECT conversation_id FROM affected);
