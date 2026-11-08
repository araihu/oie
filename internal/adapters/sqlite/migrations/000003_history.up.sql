ALTER TABLE messages ADD COLUMN created_at_ms INTEGER NOT NULL DEFAULT 0;
UPDATE messages SET created_at_ms=CAST(unixepoch(json_extract(payload,'$.CreatedAt'),'subsec')*1000 AS INTEGER);
CREATE INDEX messages_chronological ON messages(conversation_id,created_at_ms DESC,id DESC);
CREATE TABLE archive_aliases (
 account_id TEXT NOT NULL REFERENCES accounts(id), scope TEXT NOT NULL, archive_id TEXT NOT NULL,
 message_id TEXT NOT NULL REFERENCES messages(id), PRIMARY KEY(account_id,scope,archive_id)
);
CREATE TABLE archive_checkpoints (
 account_id TEXT NOT NULL REFERENCES accounts(id), scope TEXT NOT NULL, after_id TEXT NOT NULL,
 PRIMARY KEY(account_id,scope)
);
CREATE TABLE history_status (
 account_id TEXT PRIMARY KEY REFERENCES accounts(id), payload TEXT NOT NULL
);
PRAGMA user_version=3;
