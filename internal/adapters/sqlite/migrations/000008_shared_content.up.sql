CREATE TABLE shared_content_state (
 message_id TEXT PRIMARY KEY REFERENCES messages(id),
 parser_version INTEGER NOT NULL,
 caption TEXT NOT NULL
);
CREATE TABLE shared_content (
 id TEXT PRIMARY KEY, revision_id TEXT NOT NULL,
 message_id TEXT NOT NULL REFERENCES messages(id),
 url TEXT NOT NULL, kind TEXT NOT NULL, file_name TEXT NOT NULL,
 UNIQUE(message_id,url)
);
CREATE INDEX shared_content_message ON shared_content(message_id,id);
CREATE INDEX shared_content_kind ON shared_content(kind,message_id,id);
PRAGMA user_version=8;
