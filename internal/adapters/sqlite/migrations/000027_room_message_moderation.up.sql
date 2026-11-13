CREATE TABLE room_message_moderation (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    room_address TEXT NOT NULL,
    stanza_id TEXT NOT NULL,
    moderated_at TEXT NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (account_id, room_address, stanza_id)
);
PRAGMA user_version=27;
