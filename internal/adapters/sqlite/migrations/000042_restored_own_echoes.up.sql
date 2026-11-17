-- An authenticated own archive echo can identify a locally queued send
-- even when its history was excluded or its local message ID is different.
CREATE TABLE restored_own_echoes (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id TEXT NOT NULL,
    PRIMARY KEY(account_id,conversation_id,message_id)
) WITHOUT ROWID;
CREATE INDEX restored_own_echoes_conversation ON restored_own_echoes(conversation_id);
