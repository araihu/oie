CREATE TABLE message_edit_commands (
 id TEXT PRIMARY KEY,
 revision_id TEXT NOT NULL,
 message_id TEXT NOT NULL REFERENCES messages(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 state TEXT NOT NULL CHECK(state IN ('queued','dispatching','submitted','unknown','failed')),
 payload TEXT NOT NULL,
 attempt_count INTEGER NOT NULL DEFAULT 0,
 next_attempt_at TEXT NOT NULL
);
CREATE UNIQUE INDEX message_edit_active ON message_edit_commands(message_id)
WHERE state IN ('queued','dispatching','unknown');
CREATE INDEX message_edit_pending ON message_edit_commands(state,next_attempt_at,id);
CREATE INDEX original_send_receipts ON operations(resource_id) WHERE kind='send_message';
CREATE TABLE original_transmission_order (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 message_id TEXT NOT NULL REFERENCES messages(id),
 conversation_id TEXT NOT NULL REFERENCES conversations(id)
);
CREATE INDEX original_transmission_conversation ON original_transmission_order(conversation_id,ordinal DESC);
CREATE INDEX original_transmission_message ON original_transmission_order(message_id,ordinal DESC);
-- Retained original dispatch events establish monotone attempt order even when
-- authored clocks moved backwards. Corrections never create ordinary outboxes.
INSERT INTO original_transmission_order(message_id,conversation_id)
SELECT m.id,m.conversation_id FROM changes event JOIN messages m ON m.id=json_extract(event.payload,'$.ID')
WHERE event.kind='message.updated' AND json_extract(event.payload,'$.SendState')='dispatching'
AND m.is_own=1 AND EXISTS(SELECT 1 FROM operations op WHERE op.kind='send_message' AND op.resource_id=m.id)
ORDER BY event.sequence;
PRAGMA user_version=15;
