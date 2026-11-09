-- Read evidence is independent from message revisions and engine reception order.
CREATE TABLE conversation_read_revisions (
 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id),
 parents TEXT NOT NULL, payload TEXT NOT NULL, message_ids TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE conversation_read_states (
 conversation_id TEXT PRIMARY KEY REFERENCES conversations(id),
 revision_id TEXT NOT NULL REFERENCES conversation_read_revisions(id), updated_at TEXT NOT NULL
);
CREATE TABLE message_reads (
 message_id TEXT PRIMARY KEY REFERENCES messages(id),
 revision_id TEXT NOT NULL REFERENCES conversation_read_revisions(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX messages_read_candidates ON messages(conversation_id,is_own);
PRAGMA user_version=11;
