DROP INDEX xmpp_exclusions_by_room_id;
DROP TRIGGER retain_xmpp_exclusion;
CREATE TRIGGER retain_xmpp_exclusion AFTER INSERT ON expired_message_ids BEGIN
 INSERT OR IGNORE INTO xmpp_message_exclusions
 SELECT account,chat,kind,key_type,authority,remote_id FROM xmpp_message_keys WHERE message_id=NEW.id;
 INSERT OR IGNORE INTO xmpp_unidentified_removals
 SELECT a.address,c.address,json_extract(c.payload,'$.Kind'),m.created_at_ms,m.id
 FROM messages m JOIN accounts a ON a.id=m.account_id AND a.protocol='xmpp'
 JOIN conversations c ON c.id=m.conversation_id
 WHERE m.id=NEW.id AND NOT EXISTS(SELECT 1 FROM xmpp_message_keys k WHERE k.message_id=m.id);
END;
DROP VIEW observed_xmpp_message_keys;
DROP TRIGGER forget_distinct_room_keys;
DROP TRIGGER retain_distinct_room_keys;
DROP TABLE projected_room_expirations;
DROP VIEW distinct_room_message_keys;
DROP TABLE distinct_room_exclusion_keys;
PRAGMA user_version=56;
