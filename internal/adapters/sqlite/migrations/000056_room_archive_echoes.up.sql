-- The room archive UID and reflected stanza ID are the same MAM 2
-- occurrence. Keep both physical sources for revision and star ancestry.
CREATE TABLE room_archive_echo_links (
 echo_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
 original_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
 account_id TEXT NOT NULL, room_address TEXT NOT NULL, room_uid TEXT NOT NULL,
 UNIQUE(account_id,room_address,room_uid)
);
CREATE INDEX room_archive_echo_original ON room_archive_echo_links(original_id);
CREATE TRIGGER link_room_archive_echo AFTER INSERT ON room_archive_echo_links BEGIN
 INSERT INTO message_canonical_aliases(alias_id,canonical_id) VALUES(NEW.echo_id,NEW.original_id);
END;
CREATE TRIGGER unlink_room_archive_echo AFTER DELETE ON room_archive_echo_links BEGIN
 DELETE FROM message_canonical_aliases WHERE alias_id=OLD.echo_id AND canonical_id=OLD.original_id;
END;
-- A contradictory authenticated replay of the same MAM key cannot replace its
-- first physical source. Retain that decision without attaching the wrong
-- occupant or source hash to either message.
CREATE TABLE disputed_room_archive_echoes (
 message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE
);
CREATE TRIGGER dispute_room_archive_echo AFTER INSERT ON disputed_room_archive_echoes BEGIN
 DELETE FROM room_archive_echo_links WHERE echo_id=NEW.message_id;
END;
CREATE VIEW room_archive_echo_pairs AS
SELECT a.account_id, a.scope AS room_address, a.archive_id AS room_uid,
       copy.id AS echo_id, sent.id AS original_id
FROM archive_aliases a
JOIN messages copy ON copy.id=a.message_id AND copy.account_id=a.account_id
JOIN conversations c ON c.id=copy.conversation_id AND c.account_id=a.account_id AND c.address=a.scope
JOIN accounts owner ON owner.id=c.account_id AND owner.protocol='xmpp'
JOIN message_references r ON r.scope='room:'||a.scope AND r.remote_id=a.archive_id
JOIN messages sent ON sent.id=r.message_id AND sent.conversation_id=c.id AND sent.account_id=c.account_id
WHERE json_extract(c.payload,'$.Kind')='room'
  AND NOT EXISTS (SELECT 1 FROM disputed_room_archive_echoes d WHERE d.message_id=copy.id)
  AND copy.id<>sent.id AND sent.is_own=1 AND copy.id<>copy.provider_id
  AND json_extract(copy.payload,'$.Historical')=1
  AND json_extract(copy.payload,'$.ArchiveScope')=a.scope AND json_extract(copy.payload,'$.ArchiveID')=a.archive_id
  AND (EXISTS (SELECT 1 FROM legacy_room_archive_uids legacy WHERE legacy.message_id=copy.id AND legacy.archive_id=a.archive_id)
       OR EXISTS (SELECT 1 FROM message_references observed WHERE observed.message_id=copy.id
                  AND observed.scope=r.scope AND observed.remote_id=r.remote_id AND observed.author=r.author))
  AND r.author=a.scope||'/'||json_extract(copy.payload,'$.Sender.DisplayName')
  AND COALESCE(json_extract(copy.payload,'$.Sender.PersonalAddress'),'') IN ('',owner.address)
  AND NOT EXISTS (SELECT 1 FROM message_sender_identities i WHERE i.message_id IN (copy.id,sent.id)
                  AND i.personal_address<>owner.address)
  AND (sent.provider_id<>sent.id OR COALESCE(json_extract(copy.payload,'$.OwnOriginID'),'') IN ('',sent.id))
  AND r.source_hash=COALESCE(NULLIF(json_extract(sent.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(sent.payload,'$.OriginalText')))
  AND r.source_hash=COALESCE(NULLIF(json_extract(copy.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(copy.payload,'$.OriginalText')))
  AND NOT EXISTS (SELECT 1 FROM message_references x WHERE x.message_id IN (copy.id,sent.id) AND x.scope=r.scope
                  AND (x.remote_id<>r.remote_id OR x.author<>r.author OR x.source_hash<>r.source_hash))
  AND NOT EXISTS (SELECT 1 FROM message_references x CROSS JOIN messages other ON other.id=x.message_id
                  WHERE x.scope=r.scope AND x.remote_id=r.remote_id AND other.conversation_id=c.id AND other.id NOT IN (copy.id,sent.id))
  AND NOT EXISTS (SELECT 1 FROM archive_aliases x WHERE x.account_id=a.account_id AND x.scope=a.scope
                  AND x.message_id IN (sent.id,copy.id) AND x.archive_id<>a.archive_id)
  AND NOT EXISTS (SELECT 1 FROM message_correction_facts f WHERE f.message_id IN (sent.id,copy.id) AND f.target_id<>'')
  AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases edge WHERE edge.alias_id=sent.id OR edge.canonical_id=copy.id
                  OR (edge.alias_id=copy.id AND NOT EXISTS (SELECT 1 FROM room_archive_echo_links held
                      WHERE held.echo_id=copy.id AND held.original_id=sent.id AND edge.canonical_id=sent.id)));
-- Recheck only links reached by the new evidence. Removing the derived
-- alias exposes both retained sources if the room supplied conflicting facts.
CREATE TRIGGER recheck_room_echo_reference AFTER INSERT ON message_references BEGIN
 DELETE FROM room_archive_echo_links WHERE (echo_id=NEW.message_id OR original_id=NEW.message_id OR
  (account_id=(SELECT account_id FROM messages WHERE id=NEW.message_id) AND room_address=substr(NEW.scope,6) AND room_uid=NEW.remote_id))
 AND NOT EXISTS (SELECT 1 FROM room_archive_echo_pairs p WHERE p.echo_id=room_archive_echo_links.echo_id
                 AND p.original_id=room_archive_echo_links.original_id);
END;
CREATE TRIGGER recheck_room_echo_archive_key AFTER INSERT ON archive_aliases BEGIN
 DELETE FROM room_archive_echo_links WHERE (echo_id=NEW.message_id OR original_id=NEW.message_id)
 AND NOT EXISTS (SELECT 1 FROM room_archive_echo_pairs p WHERE p.echo_id=room_archive_echo_links.echo_id
                 AND p.original_id=room_archive_echo_links.original_id);
END;
CREATE TRIGGER recheck_room_echo_sender AFTER INSERT ON message_sender_identities BEGIN
 DELETE FROM room_archive_echo_links WHERE (echo_id=NEW.message_id OR original_id=NEW.message_id)
 AND NOT EXISTS (SELECT 1 FROM room_archive_echo_pairs p WHERE p.echo_id=room_archive_echo_links.echo_id
                 AND p.original_id=room_archive_echo_links.original_id);
END;
CREATE TRIGGER recheck_room_echo_correction AFTER INSERT ON message_correction_facts BEGIN
 DELETE FROM room_archive_echo_links WHERE (echo_id=NEW.message_id OR original_id=NEW.message_id)
 AND NOT EXISTS (SELECT 1 FROM room_archive_echo_pairs p WHERE p.echo_id=room_archive_echo_links.echo_id
                 AND p.original_id=room_archive_echo_links.original_id);
END;
CREATE TRIGGER recheck_room_echo_content AFTER UPDATE OF payload ON messages BEGIN
 DELETE FROM room_archive_echo_links WHERE (echo_id=NEW.id OR original_id=NEW.id)
 AND NOT EXISTS (SELECT 1 FROM room_archive_echo_pairs p WHERE p.echo_id=room_archive_echo_links.echo_id
                 AND p.original_id=room_archive_echo_links.original_id);
END;
INSERT INTO room_archive_echo_links(echo_id,original_id,account_id,room_address,room_uid)
 SELECT echo_id,original_id,account_id,room_address,room_uid FROM room_archive_echo_pairs;
PRAGMA user_version=56;
