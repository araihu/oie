CREATE TABLE room_ban_nicknames (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    nickname TEXT NOT NULL,
    PRIMARY KEY (conversation_id, address)
);
PRAGMA user_version=29;
