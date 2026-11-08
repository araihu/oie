CREATE TABLE message_correction_facts (
 message_id TEXT NOT NULL REFERENCES messages(id),
 scope TEXT NOT NULL, author TEXT NOT NULL, remote_id TEXT NOT NULL, target_id TEXT NOT NULL,
 source_hash TEXT NOT NULL, source_revision_id TEXT NOT NULL REFERENCES message_revisions(id),
 PRIMARY KEY(message_id,scope,author,remote_id,target_id,source_hash)
);
CREATE INDEX correction_source_identity ON message_correction_facts(scope,author,remote_id);
CREATE INDEX correction_target_identity ON message_correction_facts(scope,author,target_id);
CREATE TABLE message_canonical_aliases (
 alias_id TEXT PRIMARY KEY REFERENCES messages(id),
 canonical_id TEXT NOT NULL REFERENCES messages(id), CHECK(alias_id<>canonical_id)
);
CREATE INDEX correction_canonical_aliases ON message_canonical_aliases(canonical_id);
CREATE TABLE message_content_heads (
 canonical_id TEXT PRIMARY KEY REFERENCES messages(id), source_id TEXT NOT NULL REFERENCES messages(id),
 revision_id TEXT NOT NULL REFERENCES message_revisions(id), payload TEXT NOT NULL, edited_at TEXT NOT NULL
);
-- Re-read authorized archives serially once to enrich rows retained before the
-- adapter preserved sender identifiers and verified correction metadata.
UPDATE archive_checkpoints SET after_id='';
PRAGMA user_version=10;
