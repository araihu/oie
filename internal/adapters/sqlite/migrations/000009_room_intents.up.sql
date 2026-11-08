CREATE TABLE room_intents (
 conversation_id TEXT PRIMARY KEY REFERENCES conversations(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),
 id TEXT NOT NULL UNIQUE,
 revision_id TEXT NOT NULL,
 nickname TEXT NOT NULL,
 active INTEGER NOT NULL CHECK(active IN (0,1)),
 last_error_code TEXT NOT NULL,
 observation_fingerprint TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL
);
PRAGMA user_version=9;
