CREATE TABLE recovered_sends (
    message_id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    source_engine_id TEXT NOT NULL,
    revision_id TEXT NOT NULL,
    original_state TEXT NOT NULL CHECK(original_state IN ('queued','dispatching','unknown')),
    attempt_count INTEGER NOT NULL,
    scheduled_at TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL
);

-- The marker survives removal of the review item so an older backup cannot
-- put a discarded send back into review.
CREATE TABLE discarded_recovered_sends (
    message_id TEXT PRIMARY KEY
);

-- A browser can retry an already accepted operation after a restore. Reserve
-- its key while the composition is paused and restore the receipt on resume.
CREATE TABLE recovered_send_operations (
    operation_id TEXT PRIMARY KEY REFERENCES operations(id),
    message_id TEXT NOT NULL REFERENCES recovered_sends(message_id) ON DELETE CASCADE,
    result TEXT NOT NULL
);
CREATE INDEX recovered_send_operations_message ON recovered_send_operations(message_id);
