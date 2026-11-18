-- A room reference is admitted by the XMPP adapter only after SID discovery.
-- Archive aliases are bound during verified MAM ingestion. Sender-chosen
-- references have different trust semantics and are deliberately absent here.
CREATE VIEW xmpp_message_keys AS
SELECT m.id AS message_id,a.address AS account,c.address AS chat,json_extract(c.payload,'$.Kind') AS kind,
       'stanza-id' AS key_type,c.address AS authority,r.remote_id AS remote_id
FROM message_references r JOIN messages m ON m.id=r.message_id
JOIN accounts a ON a.id=m.account_id AND a.protocol='xmpp'
JOIN conversations c ON c.id=m.conversation_id AND c.account_id=a.id
WHERE json_extract(c.payload,'$.Kind')='room' AND r.scope='room:'||c.address
  AND instr(r.author,c.address||'/')=1 AND r.remote_id<>''
UNION ALL
SELECT m.id,a.address,c.address,json_extract(c.payload,'$.Kind'),'mam',r.scope,r.archive_id
FROM archive_aliases r JOIN messages m ON m.id=r.message_id AND m.account_id=r.account_id
JOIN accounts a ON a.id=m.account_id AND a.protocol='xmpp'
JOIN conversations c ON c.id=m.conversation_id AND c.account_id=a.id
WHERE r.archive_id<>'' AND (r.scope=a.address OR (json_extract(c.payload,'$.Kind')='room' AND r.scope=c.address));

CREATE TABLE xmpp_message_exclusions (
 account TEXT NOT NULL, chat TEXT NOT NULL, kind TEXT NOT NULL,
 key_type TEXT NOT NULL, authority TEXT NOT NULL, remote_id TEXT NOT NULL,
 PRIMARY KEY(account,chat,kind,key_type,authority,remote_id)
) WITHOUT ROWID;

-- A timestamp can flag a possible match after deletion when no portable
-- reference survived. It cannot authorize a merge or an automatic deletion.
CREATE TABLE xmpp_unidentified_removals (
 account TEXT NOT NULL, chat TEXT NOT NULL, kind TEXT NOT NULL, created_ms INTEGER NOT NULL,
 local_id TEXT PRIMARY KEY
);
CREATE INDEX xmpp_unidentified_removals_scope ON xmpp_unidentified_removals(account,chat,kind,created_ms);

-- purge_message_dependencies records the local exclusion before deleting the
-- references. Capture all co-observed keys while the source still exists.
CREATE TRIGGER retain_xmpp_exclusion AFTER INSERT ON expired_message_ids BEGIN
 INSERT OR IGNORE INTO xmpp_message_exclusions
 SELECT account,chat,kind,key_type,authority,remote_id FROM xmpp_message_keys WHERE message_id=NEW.id;
 INSERT OR IGNORE INTO xmpp_unidentified_removals
 SELECT a.address,c.address,json_extract(c.payload,'$.Kind'),m.created_at_ms,m.id
 FROM messages m JOIN accounts a ON a.id=m.account_id AND a.protocol='xmpp'
 JOIN conversations c ON c.id=m.conversation_id
 WHERE m.id=NEW.id AND NOT EXISTS(SELECT 1 FROM xmpp_message_keys k WHERE k.message_id=m.id);
END;
CREATE TABLE xmpp_removed_accounts (
 id TEXT PRIMARY KEY, address_hash TEXT NOT NULL
);
CREATE INDEX xmpp_removed_address ON xmpp_removed_accounts(address_hash);
CREATE TRIGGER remember_removed_xmpp_address BEFORE DELETE ON accounts WHEN OLD.protocol='xmpp' BEGIN
 INSERT OR IGNORE INTO xmpp_removed_accounts VALUES(OLD.id,oie_source_hash(OLD.address));
END;
CREATE TRIGGER forget_removed_xmpp_scopes AFTER DELETE ON accounts WHEN OLD.protocol='xmpp' BEGIN
 DELETE FROM xmpp_message_exclusions WHERE account=OLD.address;
 DELETE FROM xmpp_unidentified_removals WHERE account=OLD.address;
END;
PRAGMA user_version=53;
