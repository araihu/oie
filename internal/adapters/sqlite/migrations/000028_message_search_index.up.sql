CREATE TABLE message_search_index (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 generation TEXT NOT NULL, projection_version INTEGER NOT NULL, batch TEXT NOT NULL,
 cursor TEXT NOT NULL, ready INTEGER NOT NULL CHECK(ready IN (0,1))
);
CREATE TABLE message_search_pending (
 message_id TEXT PRIMARY KEY, token TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE message_search_exceptions (
 message_id TEXT PRIMARY KEY
) WITHOUT ROWID;
CREATE INDEX messages_global_search_timeline ON messages(COALESCE(timeline_at_ms,created_at_ms) DESC,id DESC);

-- Search eligibility is verified by SQLite at query time. The projection
-- deliberately covers hidden messages and metadata regardless of parser version.
CREATE VIEW message_search_invalidate AS SELECT '' AS message_id WHERE 0;
CREATE TRIGGER message_search_invalidate_id INSTEAD OF INSERT ON message_search_invalidate
WHEN EXISTS(SELECT 1 FROM message_search_index)
BEGIN
 INSERT INTO message_search_pending VALUES(NEW.message_id,lower(hex(randomblob(16))))
 ON CONFLICT(message_id) DO UPDATE SET token=excluded.token;
END;

CREATE TRIGGER message_search_message_insert AFTER INSERT ON messages
BEGIN
 INSERT INTO message_search_invalidate VALUES(NEW.id);
END;

CREATE TRIGGER message_search_message_delete AFTER DELETE ON messages
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.id);
END;

CREATE TRIGGER message_search_message_update AFTER UPDATE OF id, account_id, conversation_id, payload ON messages
WHEN OLD.id<>NEW.id OR OLD.account_id<>NEW.account_id OR OLD.conversation_id<>NEW.conversation_id OR json_extract(OLD.payload,'$.OriginalText') IS NOT json_extract(NEW.payload,'$.OriginalText')
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.id);
 INSERT INTO message_search_invalidate VALUES(NEW.id);
END;

CREATE TRIGGER message_search_revision_insert AFTER INSERT ON message_revisions
WHEN NEW.rowid=(SELECT min(rowid) FROM message_revisions WHERE entity_id=NEW.entity_id)
BEGIN
 INSERT INTO message_search_invalidate VALUES(NEW.entity_id);
 INSERT INTO message_search_invalidate SELECT canonical_id FROM message_canonical_aliases WHERE alias_id=NEW.entity_id;
END;

CREATE TRIGGER message_search_revision_delete AFTER DELETE ON message_revisions
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.entity_id);
 INSERT INTO message_search_invalidate SELECT canonical_id FROM message_canonical_aliases WHERE alias_id=OLD.entity_id;
END;

CREATE TRIGGER message_search_revision_update AFTER UPDATE OF entity_id, payload, rowid ON message_revisions
WHEN OLD.entity_id<>NEW.entity_id OR OLD.rowid<>NEW.rowid OR json_extract(OLD.payload,'$.OriginalText') IS NOT json_extract(NEW.payload,'$.OriginalText')
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.entity_id);
 INSERT INTO message_search_invalidate SELECT canonical_id FROM message_canonical_aliases WHERE alias_id=OLD.entity_id;
 INSERT INTO message_search_invalidate VALUES(NEW.entity_id);
 INSERT INTO message_search_invalidate SELECT canonical_id FROM message_canonical_aliases WHERE alias_id=NEW.entity_id;
END;

CREATE TRIGGER message_search_head_insert AFTER INSERT ON message_content_heads
BEGIN
 INSERT INTO message_search_invalidate VALUES(NEW.canonical_id);
END;

CREATE TRIGGER message_search_head_delete AFTER DELETE ON message_content_heads
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.canonical_id);
END;

CREATE TRIGGER message_search_head_update AFTER UPDATE OF canonical_id, payload ON message_content_heads
WHEN OLD.canonical_id<>NEW.canonical_id OR json_extract(OLD.payload,'$.OriginalText') IS NOT json_extract(NEW.payload,'$.OriginalText')
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.canonical_id);
 INSERT INTO message_search_invalidate VALUES(NEW.canonical_id);
END;

CREATE TRIGGER message_search_reference_insert AFTER INSERT ON shared_content
BEGIN
 INSERT INTO message_search_invalidate VALUES(NEW.message_id);
END;

CREATE TRIGGER message_search_reference_delete AFTER DELETE ON shared_content
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.message_id);
END;

CREATE TRIGGER message_search_reference_update AFTER UPDATE OF message_id, url, file_name ON shared_content
WHEN OLD.message_id<>NEW.message_id OR OLD.url<>NEW.url OR OLD.file_name<>NEW.file_name
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.message_id);
 INSERT INTO message_search_invalidate VALUES(NEW.message_id);
END;

CREATE TRIGGER message_search_preview_insert AFTER INSERT ON message_previews
BEGIN
 INSERT INTO message_search_invalidate VALUES(NEW.message_id);
END;

CREATE TRIGGER message_search_preview_delete AFTER DELETE ON message_previews
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.message_id);
END;

CREATE TRIGGER message_search_preview_update AFTER UPDATE OF message_id, url, payload ON message_previews
WHEN OLD.message_id<>NEW.message_id OR OLD.url<>NEW.url OR json_extract(OLD.payload,'$.Title') IS NOT json_extract(NEW.payload,'$.Title') OR json_extract(OLD.payload,'$.Description') IS NOT json_extract(NEW.payload,'$.Description')
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.message_id);
 INSERT INTO message_search_invalidate VALUES(NEW.message_id);
END;

CREATE TRIGGER message_search_alias_insert AFTER INSERT ON message_canonical_aliases
BEGIN
 INSERT INTO message_search_invalidate VALUES(NEW.canonical_id);
END;

CREATE TRIGGER message_search_alias_delete AFTER DELETE ON message_canonical_aliases
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.canonical_id);
END;

CREATE TRIGGER message_search_alias_update AFTER UPDATE ON message_canonical_aliases
BEGIN
 INSERT INTO message_search_invalidate VALUES(OLD.canonical_id);
 INSERT INTO message_search_invalidate VALUES(NEW.canonical_id);
END;

PRAGMA user_version=28;
