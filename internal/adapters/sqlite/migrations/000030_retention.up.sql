CREATE TABLE global_retention (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 rooms_days INTEGER NOT NULL CHECK(rooms_days BETWEEN 0 AND 365000),
 direct_days INTEGER NOT NULL CHECK(direct_days BETWEEN 0 AND 365000),
 revision INTEGER NOT NULL DEFAULT 0
);
INSERT INTO global_retention(singleton,rooms_days,direct_days) VALUES(1,180,0);

CREATE TABLE conversation_retention (
 conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
 days INTEGER CHECK(days BETWEEN 0 AND 365000),
 revision INTEGER NOT NULL
);

CREATE VIEW effective_retention AS
SELECT c.id AS conversation_id,c.account_id,c.address,
 CAST(json_extract(c.payload,'$.Kind') AS TEXT) AS kind,
 CAST(CASE json_extract(c.payload,'$.Kind') WHEN 'room' THEN g.rooms_days ELSE g.direct_days END AS INTEGER) AS global_days,
 CAST(COALESCE(local.days,
   CASE json_extract(c.payload,'$.Kind') WHEN 'room' THEN g.rooms_days ELSE g.direct_days END) AS INTEGER) AS days
FROM conversations c CROSS JOIN global_retention g
LEFT JOIN conversation_retention local ON local.conversation_id=c.id;

CREATE TABLE expired_message_ids (id TEXT PRIMARY KEY);
CREATE TABLE expired_message_references (
 conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 scope TEXT NOT NULL,
 remote_id TEXT NOT NULL,
 author TEXT NOT NULL,
 age_ms INTEGER NOT NULL,
 PRIMARY KEY(conversation_id,scope,remote_id,author)
);

CREATE TABLE accepted_direct_history (
 conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE
);

-- Preserve the latest confirmed send time for ordinary, scheduled and
-- rescheduled sends, independent of when an intent first appeared.
ALTER TABLE messages ADD COLUMN submitted_at_ms INTEGER;
-- Existing submitted messages have their dispatch result in revision history.
WITH submitted AS (
 SELECT entity_id, MIN(rowid) AS revision_row
 FROM message_revisions WHERE json_extract(payload,'$.SendState')='submitted'
 GROUP BY entity_id
)
UPDATE messages SET submitted_at_ms=(
 SELECT CAST(unixepoch(r.created_at,'subsec')*1000 AS INTEGER)
 FROM submitted JOIN message_revisions r ON r.rowid=submitted.revision_row
 WHERE submitted.entity_id=messages.id
)
WHERE timeline_at_ms IS NOT NULL
AND EXISTS(SELECT 1 FROM outbox WHERE message_id=messages.id AND state='submitted');

CREATE INDEX retention_message_age ON messages(conversation_id,COALESCE(submitted_at_ms,timeline_at_ms,created_at_ms) DESC,id DESC);
CREATE INDEX retention_archive_alias_message ON archive_aliases(message_id);
CREATE INDEX retention_change_message ON changes(json_extract(payload,'$.ID'))
 WHERE kind IN ('message.created','message.updated');
CREATE INDEX retention_change_conversation ON changes(json_extract(payload,'$.ID'))
 WHERE kind IN ('conversation.created','conversation.updated');
CREATE INDEX retention_change_preview ON changes(json_extract(payload,'$.MessageID'))
 WHERE kind IN ('message.preview_changed','message.star_changed');
CREATE INDEX retention_operation_resource ON operations(resource_id);
PRAGMA user_version=30;

-- Delete all content-bearing dependencies together with their source row.
CREATE TRIGGER purge_message_dependencies BEFORE DELETE ON messages BEGIN
 INSERT OR IGNORE INTO expired_message_ids VALUES(OLD.id);
 INSERT OR IGNORE INTO expired_message_references
 SELECT OLD.conversation_id,scope,remote_id,author,COALESCE(OLD.submitted_at_ms,OLD.timeline_at_ms,OLD.created_at_ms) FROM message_references WHERE message_id=OLD.id;
 INSERT OR IGNORE INTO expired_message_references
 SELECT OLD.conversation_id,scope,remote_id,author,COALESCE(OLD.submitted_at_ms,OLD.timeline_at_ms,OLD.created_at_ms) FROM message_correction_facts WHERE message_id=OLD.id;
 UPDATE operations SET result='null' WHERE resource_id=OLD.id;
 UPDATE operations SET result='null' WHERE resource_id IN (SELECT id FROM message_edit_commands WHERE message_id=OLD.id);
 UPDATE operations SET result='null' WHERE resource_id IN (SELECT id FROM message_reaction_commands WHERE message_id=OLD.id);
 UPDATE operations SET result='null' WHERE resource_id IN (SELECT id FROM message_previews WHERE message_id=OLD.id);
 DELETE FROM message_reaction_observations
 WHERE account_id=OLD.account_id
 AND address=(SELECT address FROM conversations WHERE id=OLD.conversation_id)
 AND target_id IN (SELECT remote_id FROM message_references WHERE message_id=OLD.id)
 AND EXISTS (SELECT 1 FROM message_references r WHERE r.message_id=OLD.id
      AND r.remote_id=message_reaction_observations.target_id AND r.scope=message_reaction_observations.target_scope
      AND (message_reaction_observations.target_author='' OR r.author=message_reaction_observations.target_author));
 DELETE FROM message_reaction_commands WHERE message_id=OLD.id;
 DELETE FROM message_edit_commands WHERE message_id=OLD.id;
 DELETE FROM original_transmission_order WHERE message_id=OLD.id;
 DELETE FROM message_content_heads WHERE canonical_id=OLD.id OR source_id=OLD.id;
 DELETE FROM message_canonical_aliases WHERE alias_id=OLD.id OR canonical_id=OLD.id;
 DELETE FROM message_correction_facts WHERE message_id=OLD.id;
 DELETE FROM message_references WHERE message_id=OLD.id;
 DELETE FROM message_replies WHERE message_id=OLD.id;
 DELETE FROM message_reads WHERE message_id=OLD.id;
 DELETE FROM archive_aliases WHERE message_id=OLD.id;
 DELETE FROM message_stars WHERE message_id=OLD.id;
 DELETE FROM message_star_revisions WHERE message_id=OLD.id;
 DELETE FROM preview_revisions WHERE entity_id IN (SELECT id FROM message_previews WHERE message_id=OLD.id);
 DELETE FROM message_previews WHERE message_id=OLD.id;
 DELETE FROM shared_content WHERE message_id=OLD.id;
 DELETE FROM shared_content_state WHERE message_id=OLD.id;
 DELETE FROM message_revisions WHERE entity_id=OLD.id;
 DELETE FROM outbox WHERE message_id=OLD.id;
END;
