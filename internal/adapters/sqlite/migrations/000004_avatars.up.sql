CREATE TABLE avatars (
 id TEXT PRIMARY KEY,revision_id TEXT NOT NULL,account_id TEXT NOT NULL REFERENCES accounts(id),
 address TEXT NOT NULL,payload TEXT NOT NULL,UNIQUE(account_id,address)
);
CREATE TABLE avatar_revisions (
 id TEXT PRIMARY KEY,entity_id TEXT NOT NULL REFERENCES avatars(id),parents TEXT NOT NULL,
 payload TEXT NOT NULL,created_at TEXT NOT NULL
);
PRAGMA user_version=4;
