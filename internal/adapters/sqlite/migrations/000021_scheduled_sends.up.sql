ALTER TABLE outbox ADD COLUMN scheduled_at TEXT NOT NULL DEFAULT '';
CREATE INDEX outbox_schedule ON outbox(scheduled_at, message_id)
WHERE state = 'queued' AND scheduled_at <> '';

CREATE TABLE staged_attachments (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    file_name TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size INTEGER NOT NULL CHECK(size >= 0),
    sha256 TEXT NOT NULL
);
