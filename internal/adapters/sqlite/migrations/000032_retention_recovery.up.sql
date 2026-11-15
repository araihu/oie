ALTER TABLE global_retention ADD COLUMN archive_revision INTEGER NOT NULL DEFAULT 0;
CREATE TABLE account_archive_retention (
 account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL
);
CREATE TABLE discarded_message_references (
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 address TEXT NOT NULL,
 kind TEXT NOT NULL,
 scope TEXT NOT NULL,
 remote_id TEXT NOT NULL,
 author TEXT NOT NULL,
 age_ms INTEGER NOT NULL,
 PRIMARY KEY(account_id,address,kind,scope,remote_id,author)
);
PRAGMA user_version=32;
