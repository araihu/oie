CREATE TABLE message_references (
 message_id TEXT NOT NULL REFERENCES messages(id),
 scope TEXT NOT NULL, remote_id TEXT NOT NULL, author TEXT NOT NULL,
 source_hash TEXT NOT NULL,
 PRIMARY KEY(message_id,scope,remote_id,author,source_hash)
);
CREATE INDEX message_references_lookup ON message_references(remote_id,scope,author,message_id);
CREATE TABLE message_replies (
 message_id TEXT NOT NULL REFERENCES messages(id), body_hash TEXT NOT NULL,
 payload TEXT NOT NULL, PRIMARY KEY(message_id,body_hash)
);
-- Earlier readers discarded reply references. Replay authorized archives once
-- through existing aliases to enrich retained messages without replacing them.
UPDATE archive_checkpoints SET after_id='';
PRAGMA user_version=7;
