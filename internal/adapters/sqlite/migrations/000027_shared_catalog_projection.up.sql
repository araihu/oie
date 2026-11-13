-- This projection is rebuilt from canonical messages and the existing shared
-- references. Reference IDs remain the public identity and pagination tie-break.
CREATE TABLE shared_catalog_sources (
 message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
 conversation_id TEXT NOT NULL,
 position_ms INTEGER NOT NULL,
 parser_version INTEGER NOT NULL
);
CREATE INDEX shared_catalog_pending ON shared_catalog_sources(parser_version,position_ms DESC,message_id DESC);
CREATE TABLE shared_catalog_counts (
 conversation_id TEXT NOT NULL,
 parser_version INTEGER NOT NULL,
 messages INTEGER NOT NULL,
 PRIMARY KEY(conversation_id,parser_version)
) WITHOUT ROWID;

-- Each reference has one ordered entry per browsing scope. An equality seek
-- on scope and tab can then walk the page in cursor order.
CREATE TABLE shared_catalog_order (
 scope TEXT NOT NULL,
 tab TEXT NOT NULL,
 position_ms INTEGER NOT NULL,
 reference_id TEXT NOT NULL REFERENCES shared_content(id) ON DELETE CASCADE,
 PRIMARY KEY(scope,tab,position_ms,reference_id)
) WITHOUT ROWID;
CREATE INDEX shared_catalog_order_reference ON shared_catalog_order(reference_id);

CREATE VIEW shared_catalog_visible_sources AS
SELECT m.id AS message_id,m.conversation_id,COALESCE(m.timeline_at_ms,m.created_at_ms) AS position_ms,
 COALESCE(s.parser_version,-1) AS parser_version
FROM messages m LEFT JOIN shared_content_state s ON s.message_id=m.id
WHERE EXISTS (SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id);

CREATE VIEW shared_catalog_order_rows AS
WITH kinds AS (
 SELECT sc.id AS reference_id,sc.message_id,m.account_id,s.conversation_id,s.position_ms,
 CASE WHEN json_extract(p.payload,'$.Kind')='image' OR sc.kind IN ('image','video','audio') THEN 'media'
      WHEN sc.kind='link' THEN 'links' WHEN sc.kind='document' THEN 'documents' END AS tab
 FROM shared_content sc JOIN shared_catalog_sources s ON s.message_id=sc.message_id
 JOIN messages m ON m.id=sc.message_id LEFT JOIN message_previews p ON p.message_id=m.id AND p.url=sc.url
 WHERE s.parser_version>=0
)
SELECT CASE scope.kind WHEN 0 THEN '' WHEN 1 THEN 'account:'||account_id ELSE 'conversation:'||conversation_id END AS scope,
 tab,position_ms,reference_id,message_id
FROM kinds CROSS JOIN (SELECT 0 AS kind UNION ALL SELECT 1 UNION ALL SELECT 2) scope
WHERE tab IS NOT NULL;

INSERT INTO shared_catalog_sources SELECT * FROM shared_catalog_visible_sources;
INSERT INTO shared_catalog_counts
 SELECT conversation_id,parser_version,COUNT(*) FROM shared_catalog_sources GROUP BY conversation_id,parser_version;
INSERT INTO shared_catalog_order
 SELECT scope,tab,position_ms,reference_id FROM shared_catalog_order_rows;

CREATE TRIGGER shared_catalog_count_insert AFTER INSERT ON shared_catalog_sources BEGIN
 INSERT INTO shared_catalog_counts VALUES(NEW.conversation_id,NEW.parser_version,1)
 ON CONFLICT(conversation_id,parser_version) DO UPDATE SET messages=messages+1;
END;
CREATE TRIGGER shared_catalog_count_delete AFTER DELETE ON shared_catalog_sources BEGIN
 UPDATE shared_catalog_counts SET messages=messages-1
 WHERE conversation_id=OLD.conversation_id AND parser_version=OLD.parser_version;
 DELETE FROM shared_catalog_counts
 WHERE conversation_id=OLD.conversation_id AND parser_version=OLD.parser_version AND messages=0;
END;
CREATE TRIGGER shared_catalog_count_update AFTER UPDATE OF parser_version,conversation_id ON shared_catalog_sources
WHEN OLD.parser_version<>NEW.parser_version OR OLD.conversation_id<>NEW.conversation_id BEGIN
 UPDATE shared_catalog_counts SET messages=messages-1
 WHERE conversation_id=OLD.conversation_id AND parser_version=OLD.parser_version;
 DELETE FROM shared_catalog_counts
 WHERE conversation_id=OLD.conversation_id AND parser_version=OLD.parser_version AND messages=0;
 INSERT INTO shared_catalog_counts VALUES(NEW.conversation_id,NEW.parser_version,1)
 ON CONFLICT(conversation_id,parser_version) DO UPDATE SET messages=messages+1;
END;

-- Reconcile one source in the same transaction as the canonical change.
CREATE VIEW shared_catalog_refresh AS SELECT '' AS message_id WHERE 0;
CREATE TRIGGER shared_catalog_refresh_source INSTEAD OF INSERT ON shared_catalog_refresh BEGIN
 DELETE FROM shared_catalog_sources WHERE message_id=NEW.message_id
 AND NOT EXISTS (SELECT 1 FROM shared_catalog_visible_sources WHERE message_id=NEW.message_id);
 INSERT INTO shared_catalog_sources SELECT * FROM shared_catalog_visible_sources WHERE message_id=NEW.message_id
 ON CONFLICT(message_id) DO UPDATE SET conversation_id=excluded.conversation_id,
 position_ms=excluded.position_ms,parser_version=excluded.parser_version;
 DELETE FROM shared_catalog_order WHERE reference_id IN (SELECT id FROM shared_content WHERE message_id=NEW.message_id);
 INSERT INTO shared_catalog_order SELECT scope,tab,position_ms,reference_id FROM shared_catalog_order_rows WHERE message_id=NEW.message_id;
END;
CREATE TRIGGER shared_catalog_message_insert AFTER INSERT ON messages BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.id);
END;
CREATE TRIGGER shared_catalog_message_position AFTER UPDATE OF timeline_at_ms,created_at_ms ON messages
WHEN OLD.timeline_at_ms IS NOT NEW.timeline_at_ms OR OLD.created_at_ms<>NEW.created_at_ms BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.id);
END;
CREATE TRIGGER shared_catalog_state_insert AFTER INSERT ON shared_content_state BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.message_id);
END;
CREATE TRIGGER shared_catalog_state_update AFTER UPDATE OF parser_version ON shared_content_state BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.message_id);
END;
CREATE TRIGGER shared_catalog_state_delete AFTER DELETE ON shared_content_state BEGIN
 INSERT INTO shared_catalog_refresh VALUES(OLD.message_id);
END;
CREATE TRIGGER shared_catalog_reference_insert AFTER INSERT ON shared_content BEGIN
 INSERT INTO shared_catalog_order SELECT scope,tab,position_ms,reference_id FROM shared_catalog_order_rows WHERE reference_id=NEW.id;
END;
CREATE TRIGGER shared_catalog_reference_update AFTER UPDATE OF kind ON shared_content WHEN OLD.kind<>NEW.kind BEGIN
 DELETE FROM shared_catalog_order WHERE reference_id=OLD.id;
 INSERT INTO shared_catalog_order SELECT scope,tab,position_ms,reference_id FROM shared_catalog_order_rows WHERE reference_id=NEW.id;
END;
CREATE TRIGGER shared_catalog_preview_insert AFTER INSERT ON message_previews
WHEN json_extract(NEW.payload,'$.Kind')='image' BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.message_id);
END;
CREATE TRIGGER shared_catalog_preview_update AFTER UPDATE OF payload ON message_previews
WHEN (json_extract(OLD.payload,'$.Kind') IS 'image')<>(json_extract(NEW.payload,'$.Kind') IS 'image') BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.message_id);
END;
CREATE TRIGGER shared_catalog_preview_delete AFTER DELETE ON message_previews
WHEN json_extract(OLD.payload,'$.Kind')='image' BEGIN
 INSERT INTO shared_catalog_refresh VALUES(OLD.message_id);
END;
CREATE TRIGGER shared_catalog_alias_insert AFTER INSERT ON message_canonical_aliases BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.alias_id);
END;
CREATE TRIGGER shared_catalog_alias_delete AFTER DELETE ON message_canonical_aliases BEGIN
 INSERT INTO shared_catalog_refresh VALUES(OLD.alias_id);
END;
CREATE TRIGGER shared_catalog_outbox_insert AFTER INSERT ON outbox WHEN NEW.scheduled_at<>'' BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.message_id);
END;
CREATE TRIGGER shared_catalog_outbox_update AFTER UPDATE OF state,scheduled_at ON outbox
WHEN (OLD.state<>NEW.state OR OLD.scheduled_at<>NEW.scheduled_at)
AND (OLD.scheduled_at<>'' OR NEW.scheduled_at<>'' OR OLD.state='cancelled' OR NEW.state='cancelled') BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.message_id);
END;
CREATE TRIGGER shared_catalog_outbox_delete AFTER DELETE ON outbox
WHEN OLD.scheduled_at<>'' OR OLD.state='cancelled' BEGIN
 INSERT INTO shared_catalog_refresh VALUES(OLD.message_id);
END;
CREATE TRIGGER shared_catalog_request_update AFTER UPDATE OF payload ON conversations
WHEN (json_extract(OLD.payload,'$.Request') IS NULL)<>(json_extract(NEW.payload,'$.Request') IS NULL) BEGIN
 INSERT INTO shared_catalog_refresh SELECT id FROM messages WHERE conversation_id=NEW.id;
END;

PRAGMA user_version=27;
