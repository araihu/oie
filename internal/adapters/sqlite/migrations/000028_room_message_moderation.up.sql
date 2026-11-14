CREATE TABLE IF NOT EXISTS room_message_moderation (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    room_address TEXT NOT NULL,
    stanza_id TEXT NOT NULL,
    moderated_at TEXT NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (account_id, room_address, stanza_id)
);

DROP VIEW shared_catalog_visible_sources;
CREATE VIEW shared_catalog_visible_sources AS
SELECT m.id AS message_id,m.conversation_id,COALESCE(m.timeline_at_ms,m.created_at_ms) AS position_ms,
 COALESCE(s.parser_version,-1) AS parser_version
FROM messages m LEFT JOIN shared_content_state s ON s.message_id=m.id
WHERE EXISTS (SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
AND NOT EXISTS (
 SELECT 1 FROM message_references ref JOIN room_message_moderation moderation
 ON moderation.account_id=m.account_id AND moderation.room_address=substr(ref.scope,6) AND moderation.stanza_id=ref.remote_id
 WHERE substr(ref.scope,1,5)='room:'
 AND (ref.message_id=m.id OR ref.message_id IN (SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=m.id))
);

CREATE VIEW shared_catalog_moderated_sources AS
SELECT DISTINCT moderation.account_id, moderation.room_address, moderation.stanza_id,
 COALESCE(alias.canonical_id, ref.message_id) AS message_id
FROM room_message_moderation moderation
JOIN message_references ref ON ref.remote_id=moderation.stanza_id AND ref.scope='room:'||moderation.room_address
JOIN messages source ON source.id=ref.message_id AND source.account_id=moderation.account_id
LEFT JOIN message_canonical_aliases alias ON alias.alias_id=ref.message_id;

CREATE TRIGGER shared_catalog_moderation_insert AFTER INSERT ON room_message_moderation BEGIN
 INSERT INTO shared_catalog_refresh
 SELECT message_id FROM shared_catalog_moderated_sources
 WHERE account_id=NEW.account_id AND room_address=NEW.room_address AND stanza_id=NEW.stanza_id;
END;
CREATE TRIGGER shared_catalog_moderated_reference_insert AFTER INSERT ON message_references
WHEN substr(NEW.scope,1,5)='room:' AND EXISTS (
 SELECT 1 FROM room_message_moderation
 WHERE account_id=(SELECT account_id FROM messages WHERE id=NEW.message_id)
 AND room_address=substr(NEW.scope,6) AND stanza_id=NEW.remote_id
) BEGIN
 INSERT INTO shared_catalog_refresh
 VALUES(COALESCE((SELECT canonical_id FROM message_canonical_aliases WHERE alias_id=NEW.message_id),NEW.message_id));
END;
CREATE TRIGGER shared_catalog_moderated_alias_insert AFTER INSERT ON message_canonical_aliases
WHEN EXISTS (
 SELECT 1 FROM message_references ref JOIN room_message_moderation moderation
 ON moderation.account_id=(SELECT account_id FROM messages WHERE id=NEW.alias_id)
 AND moderation.room_address=substr(ref.scope,6) AND moderation.stanza_id=ref.remote_id
 WHERE ref.message_id=NEW.alias_id AND substr(ref.scope,1,5)='room:'
) BEGIN
 INSERT INTO shared_catalog_refresh VALUES(NEW.canonical_id);
END;
CREATE TRIGGER shared_catalog_moderated_alias_delete AFTER DELETE ON message_canonical_aliases BEGIN
 INSERT INTO shared_catalog_refresh VALUES(OLD.canonical_id);
END;

INSERT INTO shared_catalog_refresh SELECT DISTINCT message_id FROM shared_catalog_moderated_sources;
PRAGMA user_version=28;
