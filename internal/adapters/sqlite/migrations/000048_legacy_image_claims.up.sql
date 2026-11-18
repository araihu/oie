CREATE TABLE legacy_image_claims (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    hash TEXT NOT NULL,
    size INTEGER NOT NULL CHECK(size >= 0),
    PRIMARY KEY(message_id,url,source_revision,hash,size)
) WITHOUT ROWID;
