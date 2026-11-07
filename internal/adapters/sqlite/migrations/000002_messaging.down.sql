-- A downgrade discards messaging data; the app never runs down migrations.
DROP TABLE outbox;
DROP TABLE message_revisions;
DROP TABLE messages;
DROP TABLE conversation_revisions;
DROP TABLE conversations;
ALTER TABLE changes DROP COLUMN kind;
CREATE TABLE account_operations (
 id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL,
 resource_id TEXT NOT NULL REFERENCES accounts(id) DEFERRABLE INITIALLY DEFERRED,
 result TEXT NOT NULL, created_at TEXT NOT NULL
);
INSERT INTO account_operations SELECT id,fingerprint,resource_id,result,created_at FROM operations WHERE kind='register_account';
DROP TABLE operations;
ALTER TABLE account_operations RENAME TO operations;
PRAGMA user_version = 1;
