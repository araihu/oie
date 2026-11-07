-- Operation resources now include conversations/messages, not just accounts.
CREATE TABLE operation_receipts (
 id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, resource_id TEXT NOT NULL,
 result TEXT NOT NULL, created_at TEXT NOT NULL, kind TEXT NOT NULL
);
INSERT INTO operation_receipts SELECT id,fingerprint,resource_id,result,created_at,'register_account' FROM operations;
DROP TABLE operations;
ALTER TABLE operation_receipts RENAME TO operations;
ALTER TABLE changes ADD COLUMN kind TEXT NOT NULL DEFAULT 'account.registered';

CREATE TABLE conversations (
 id TEXT PRIMARY KEY, revision_id TEXT NOT NULL, account_id TEXT NOT NULL REFERENCES accounts(id),
 address TEXT NOT NULL, peer_id TEXT NOT NULL, payload TEXT NOT NULL, activity_sequence INTEGER NOT NULL,
 unread_count INTEGER NOT NULL DEFAULT 0, archived INTEGER NOT NULL DEFAULT 0, favorite INTEGER NOT NULL DEFAULT 0,
 UNIQUE(account_id,address)
);
CREATE TABLE conversation_revisions (
 id TEXT PRIMARY KEY, entity_id TEXT NOT NULL REFERENCES conversations(id) DEFERRABLE INITIALLY DEFERRED,
 parents TEXT NOT NULL, payload TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE messages (
 id TEXT PRIMARY KEY, revision_id TEXT NOT NULL, account_id TEXT NOT NULL REFERENCES accounts(id),
 conversation_id TEXT NOT NULL REFERENCES conversations(id), provider_id TEXT NOT NULL,
 is_own INTEGER NOT NULL, state TEXT NOT NULL, payload TEXT NOT NULL
);
CREATE UNIQUE INDEX messages_provider_identity ON messages(account_id,conversation_id,provider_id,is_own) WHERE provider_id <> '';
CREATE INDEX messages_conversation_page ON messages(conversation_id,id DESC);
CREATE TABLE message_revisions (
 id TEXT PRIMARY KEY, entity_id TEXT NOT NULL REFERENCES messages(id) DEFERRABLE INITIALLY DEFERRED,
 parents TEXT NOT NULL, payload TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE outbox (
 message_id TEXT PRIMARY KEY REFERENCES messages(id), state TEXT NOT NULL,
 attempt_count INTEGER NOT NULL DEFAULT 0, next_attempt_at TEXT NOT NULL
);
PRAGMA user_version = 2;
