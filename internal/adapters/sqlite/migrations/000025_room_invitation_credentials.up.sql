CREATE TABLE room_invitation_credentials (
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    password TEXT NOT NULL
);
