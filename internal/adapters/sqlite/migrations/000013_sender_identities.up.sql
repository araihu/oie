-- Identity evidence is separate from immutable message content/revisions.
-- Two different observations permanently suppress a source profile rather than
-- replacing one address with the most recent observation.
CREATE TABLE message_sender_identities (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    personal_address TEXT NOT NULL CHECK(length(personal_address) BETWEEN 1 AND 1024),
    source_hash TEXT NOT NULL CHECK(length(source_hash)=64),
    PRIMARY KEY(message_id,personal_address,source_hash)
);
