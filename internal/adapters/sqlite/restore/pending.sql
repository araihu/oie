CREATE TEMP TABLE restore_pending_candidates (
    message_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, source_engine_id TEXT NOT NULL,
    revision_id TEXT NOT NULL, original_state TEXT NOT NULL, attempt_count INTEGER NOT NULL,
    scheduled_at TEXT NOT NULL, payload TEXT NOT NULL, created_at_ms INTEGER NOT NULL
) WITHOUT ROWID;

INSERT INTO restore_pending_candidates
SELECT m.id,m.conversation_id,@source_engine,m.revision_id,o.state,o.attempt_count,o.scheduled_at,m.payload,m.created_at_ms
FROM backup.messages m JOIN backup.outbox o ON o.message_id=m.id
JOIN restore_chats c ON c.id=m.conversation_id
WHERE o.state IN ('queued','dispatching','unknown') AND m.is_own=1;
