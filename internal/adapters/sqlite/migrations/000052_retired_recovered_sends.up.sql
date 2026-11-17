CREATE TABLE retired_recovered_sends (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id TEXT NOT NULL,
    PRIMARY KEY(account_id,conversation_id,message_id)
) WITHOUT ROWID;
