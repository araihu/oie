-- Some rooms supply different identifiers for the live stanza and the MAM
-- result. Retain that distinction after the body and its keys are purged.
CREATE INDEX xmpp_exclusions_by_room_id ON xmpp_message_exclusions(account,chat,kind,remote_id);
CREATE TABLE distinct_room_exclusion_keys (
 account TEXT NOT NULL, chat TEXT NOT NULL, key_type TEXT NOT NULL, remote_id TEXT NOT NULL,
 PRIMARY KEY(account,chat,key_type,remote_id)
) WITHOUT ROWID;
-- A verified MAM result can supply its UID as a missing inner stanza ID.
-- Preserve the archive key as the observed form when the two are equal.
CREATE VIEW observed_xmpp_message_keys AS
SELECT k.* FROM xmpp_message_keys k WHERE k.key_type<>'stanza-id' OR NOT EXISTS (
 SELECT 1 FROM archive_aliases a WHERE a.message_id=k.message_id AND a.scope=k.chat AND a.archive_id=k.remote_id
);
DROP TRIGGER retain_xmpp_exclusion;
CREATE TRIGGER retain_xmpp_exclusion AFTER INSERT ON expired_message_ids BEGIN
 INSERT OR IGNORE INTO xmpp_message_exclusions
 SELECT account,chat,kind,key_type,authority,remote_id FROM observed_xmpp_message_keys WHERE message_id=NEW.id;
 INSERT OR IGNORE INTO xmpp_unidentified_removals
 SELECT a.address,c.address,json_extract(c.payload,'$.Kind'),m.created_at_ms,m.id
 FROM messages m JOIN accounts a ON a.id=m.account_id AND a.protocol='xmpp'
 JOIN conversations c ON c.id=m.conversation_id
 WHERE m.id=NEW.id AND NOT EXISTS(SELECT 1 FROM xmpp_message_keys k WHERE k.message_id=m.id);
END;
CREATE VIEW distinct_room_message_keys AS
SELECT k.message_id,k.account,k.chat,k.key_type,k.remote_id FROM xmpp_message_keys k
WHERE k.kind='room' AND k.authority=k.chat AND EXISTS (
 SELECT 1 FROM xmpp_message_keys other WHERE other.message_id=k.message_id
 AND other.authority=k.chat AND other.key_type<>k.key_type AND other.remote_id<>k.remote_id
);
-- An expired reply reference can originate from a projected stanza ID.
-- Keep that provenance separately from legacy, directly observed references.
CREATE TABLE projected_room_expirations (
 account TEXT NOT NULL, room TEXT NOT NULL, uid TEXT NOT NULL, author TEXT NOT NULL, age_ms INTEGER NOT NULL,
 PRIMARY KEY(account,room,uid,author,age_ms)
) WITHOUT ROWID;
CREATE TRIGGER retain_distinct_room_keys AFTER INSERT ON expired_message_ids BEGIN
 INSERT OR IGNORE INTO distinct_room_exclusion_keys
 SELECT account,chat,key_type,remote_id FROM distinct_room_message_keys WHERE message_id=NEW.id;
 DELETE FROM projected_room_expirations
 WHERE EXISTS (SELECT 1 FROM observed_xmpp_message_keys k JOIN message_references ref ON ref.message_id=k.message_id
   WHERE k.message_id=NEW.id AND k.key_type='stanza-id' AND k.account=projected_room_expirations.account AND k.chat=projected_room_expirations.room
   AND k.remote_id=uid AND ref.scope='room:'||room AND ref.author=projected_room_expirations.author AND ref.remote_id=uid);
 INSERT OR IGNORE INTO projected_room_expirations(account,room,uid,author,age_ms)
 SELECT k.account,k.chat,k.remote_id,r.author,COALESCE(m.submitted_at_ms,m.timeline_at_ms,m.created_at_ms)
 FROM xmpp_message_keys k JOIN messages m ON m.id=k.message_id JOIN message_references r ON r.message_id=m.id
 WHERE m.id=NEW.id AND k.key_type='stanza-id' AND r.scope='room:'||k.chat AND r.remote_id=k.remote_id
 AND EXISTS (SELECT 1 FROM archive_aliases a WHERE a.message_id=m.id AND a.scope=k.chat AND a.archive_id=k.remote_id)
 AND NOT EXISTS (SELECT 1 FROM expired_message_references prev WHERE prev.conversation_id=m.conversation_id
                 AND prev.scope=r.scope AND prev.remote_id=r.remote_id AND prev.author=r.author);
END;
CREATE TRIGGER forget_distinct_room_keys AFTER DELETE ON accounts WHEN OLD.protocol='xmpp' BEGIN
 DELETE FROM distinct_room_exclusion_keys WHERE account=OLD.address;
 DELETE FROM projected_room_expirations WHERE account=OLD.address;
END;
-- Previous profiles stored deletion keys without the association between
-- them. Only infer equivalence when there is no possible contradictory key.
INSERT INTO distinct_room_exclusion_keys
SELECT e.account,e.chat,e.key_type,e.remote_id FROM xmpp_message_exclusions e
WHERE e.kind='room' AND e.authority=e.chat AND EXISTS (
 SELECT 1 FROM xmpp_message_exclusions other WHERE other.account=e.account AND other.chat=e.chat
 AND other.kind=e.kind AND other.authority=e.authority
 AND other.key_type=CASE e.key_type WHEN 'mam' THEN 'stanza-id' ELSE 'mam' END AND other.remote_id<>e.remote_id
);
PRAGMA user_version=57;
