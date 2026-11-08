CREATE TABLE message_previews (
 id TEXT PRIMARY KEY, revision_id TEXT NOT NULL,
 account_id TEXT NOT NULL REFERENCES accounts(id),
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 message_id TEXT NOT NULL REFERENCES messages(id),
 url TEXT NOT NULL, state TEXT NOT NULL, download_state TEXT NOT NULL,
 updated_at TEXT NOT NULL, payload TEXT NOT NULL,
 UNIQUE(message_id,url)
);
CREATE INDEX message_previews_conversation ON message_previews(conversation_id,message_id);
CREATE INDEX message_previews_jobs ON message_previews(download_state,state,updated_at);
CREATE TABLE preview_revisions (
 id TEXT PRIMARY KEY,entity_id TEXT NOT NULL REFERENCES message_previews(id),
 parents TEXT NOT NULL,payload TEXT NOT NULL,created_at TEXT NOT NULL
);
CREATE TABLE media_files (
 hash TEXT PRIMARY KEY,mime_type TEXT NOT NULL,size INTEGER NOT NULL,created_at TEXT NOT NULL
);
-- Earlier room imports discarded authenticated MUC author metadata. Replay the
-- available room archive once; aliases retain message IDs and prevent new sends.
UPDATE archive_checkpoints SET after_id='' WHERE EXISTS (
 SELECT 1 FROM conversations c WHERE c.account_id=archive_checkpoints.account_id
 AND c.address=archive_checkpoints.scope AND json_extract(c.payload,'$.Kind')='room'
);
PRAGMA user_version=5;
