-- Older versions retained the room's MAM 2 UID but sometimes missed its
-- stanza-id. A verified room reply or room-issued reference proves SID
-- support in this account and room. Recover unambiguous occupants only.
CREATE TABLE legacy_room_archive_uids (
 message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
 archive_id TEXT NOT NULL, PRIMARY KEY(message_id,archive_id)
) WITHOUT ROWID;
INSERT INTO legacy_room_archive_uids
SELECT DISTINCT a.message_id,a.archive_id FROM archive_aliases a
JOIN messages m ON m.id=a.message_id
JOIN conversations c ON c.id=m.conversation_id AND c.account_id=a.account_id AND c.address=a.scope
WHERE json_extract(c.payload,'$.Kind')='room'
AND NOT EXISTS (SELECT 1 FROM message_references r WHERE r.message_id=m.id AND r.scope='room:'||a.scope);

CREATE VIEW legacy_room_reply_candidates AS
SELECT m.id AS message_id,m.conversation_id,'room:'||c.address AS scope,a.archive_id AS remote_id,
 c.address||'/'||json_extract(m.payload,'$.Sender.DisplayName') AS author,
 COALESCE(NULLIF(json_extract(m.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(m.payload,'$.OriginalText'))) AS source_hash
FROM legacy_room_archive_uids legacy
JOIN archive_aliases a ON a.message_id=legacy.message_id AND a.archive_id=legacy.archive_id
JOIN messages m ON m.id=a.message_id AND m.account_id=a.account_id
JOIN conversations c ON c.id=m.conversation_id AND c.account_id=m.account_id AND c.address=a.scope
JOIN accounts account ON account.id=m.account_id AND account.protocol='xmpp'
WHERE json_extract(c.payload,'$.Kind')='room' AND c.address<>account.address AND a.archive_id<>''
 AND COALESCE(json_extract(m.payload,'$.Sender.DisplayName'),'')<>''
 -- Native sends retain the account label rather than the reflected occupant.
 AND (m.is_own=0 OR m.provider_id LIKE 'room:%' OR
     (json_extract(m.payload,'$.ArchiveScope')=a.scope AND json_extract(m.payload,'$.ArchiveID')=a.archive_id))
 AND NOT EXISTS (SELECT 1 FROM message_references r WHERE r.message_id=m.id AND r.scope='room:'||a.scope)
 AND NOT EXISTS (SELECT 1 FROM archive_aliases other WHERE other.message_id=m.id AND other.scope=a.scope AND other.archive_id<>a.archive_id)
 AND NOT EXISTS (
   SELECT 1 FROM message_references r CROSS JOIN messages other ON other.id=r.message_id
   WHERE r.remote_id=a.archive_id AND r.scope='room:'||a.scope AND other.conversation_id=c.id
 );

CREATE VIEW verified_legacy_room_reply_targets AS
WITH verified_rooms AS (
 SELECT DISTINCT m.conversation_id FROM message_references r JOIN messages m ON m.id=r.message_id
 JOIN conversations c ON c.id=m.conversation_id
 WHERE r.scope='room:'||c.address AND instr(r.author,c.address||'/')=1
 UNION
 SELECT DISTINCT m.conversation_id FROM message_replies r JOIN messages m ON m.id=r.message_id
 JOIN conversations c ON c.id=m.conversation_id
 WHERE json_extract(r.payload,'$.Target.Scope')='room:'||c.address
)
SELECT p.message_id,p.scope,p.remote_id,p.author,p.source_hash FROM legacy_room_reply_candidates p
JOIN verified_rooms known ON known.conversation_id=p.conversation_id;
INSERT OR IGNORE INTO message_references(message_id,scope,remote_id,author,source_hash)
SELECT * FROM verified_legacy_room_reply_targets;

-- A later verified room reply can establish discovery for an older profile.
-- Restrict the recovery to its specific original and observed occupant.
CREATE TRIGGER resolve_legacy_room_reply AFTER INSERT ON message_replies
WHEN substr(json_extract(NEW.payload,'$.Target.Scope'),1,5)='room:'
BEGIN
 INSERT OR IGNORE INTO message_references(message_id,scope,remote_id,author,source_hash)
 SELECT p.message_id,p.scope,p.remote_id,p.author,p.source_hash FROM legacy_room_reply_candidates p
 JOIN messages reply ON reply.id=NEW.message_id AND reply.conversation_id=p.conversation_id
 WHERE p.scope=json_extract(NEW.payload,'$.Target.Scope') AND p.remote_id=json_extract(NEW.payload,'$.Target.ID')
 AND (COALESCE(json_extract(NEW.payload,'$.Target.Author'),'')='' OR p.author=json_extract(NEW.payload,'$.Target.Author'));
END;
-- A legacy archive alias can contradict an explicit stanza-id on a different
-- message. The same ambiguity must disable reading and sending a reply.
CREATE VIEW room_archive_reference_conflicts AS
SELECT r.message_id,r.scope,r.remote_id
FROM message_references r JOIN messages m ON m.id=r.message_id
JOIN archive_aliases a ON a.account_id=m.account_id AND a.scope=substr(r.scope,6) AND a.archive_id=r.remote_id
JOIN messages archived ON archived.id=a.message_id AND archived.conversation_id=m.conversation_id
LEFT JOIN message_canonical_aliases old ON old.alias_id=archived.id
LEFT JOIN message_canonical_aliases selected ON selected.alias_id=m.id
WHERE substr(r.scope,1,5)='room:'
 AND COALESCE(old.canonical_id,archived.id)<>COALESCE(selected.canonical_id,m.id)
 AND (EXISTS (SELECT 1 FROM legacy_room_archive_uids legacy WHERE legacy.message_id=archived.id AND legacy.archive_id=a.archive_id)
      OR EXISTS (SELECT 1 FROM message_references valid WHERE valid.message_id=archived.id
                 AND valid.scope=r.scope AND valid.remote_id=a.archive_id))
 AND NOT EXISTS (SELECT 1 FROM message_references explicit WHERE explicit.message_id=archived.id
                 AND explicit.scope=r.scope AND explicit.remote_id<>a.archive_id);
PRAGMA user_version=55;
