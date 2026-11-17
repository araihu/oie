ALTER TABLE account_communication_preferences ADD COLUMN send_read_receipts INTEGER NOT NULL DEFAULT 1 CHECK (send_read_receipts IN (0, 1));

CREATE INDEX message_reads_by_revision ON message_reads(revision_id, message_id);

CREATE TABLE conversation_displayed_heads (
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    position_ms INTEGER NOT NULL,
    message_id TEXT NOT NULL
);
