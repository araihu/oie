DROP TRIGGER shared_catalog_moderation_insert;
DROP TRIGGER shared_catalog_moderated_reference_insert;
DROP TRIGGER shared_catalog_moderated_alias_insert;
DROP TRIGGER shared_catalog_moderated_alias_delete;

DROP VIEW shared_catalog_visible_sources;
CREATE VIEW shared_catalog_visible_sources AS
SELECT m.id AS message_id,m.conversation_id,COALESCE(m.timeline_at_ms,m.created_at_ms) AS position_ms,
 COALESCE(s.parser_version,-1) AS parser_version
FROM messages m LEFT JOIN shared_content_state s ON s.message_id=m.id
WHERE EXISTS (SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id);
INSERT INTO shared_catalog_refresh SELECT DISTINCT message_id FROM shared_catalog_moderated_sources;
DROP VIEW shared_catalog_moderated_sources;
DROP TABLE room_message_moderation;
PRAGMA user_version=27;
