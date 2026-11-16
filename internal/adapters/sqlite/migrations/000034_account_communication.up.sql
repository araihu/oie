CREATE TABLE account_communication_preferences (
    account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    revision_id TEXT NOT NULL,
    presence TEXT NOT NULL CHECK (presence IN ('available', 'away', 'dnd')),
    send_typing INTEGER NOT NULL DEFAULT 1 CHECK (send_typing IN (0, 1))
);
