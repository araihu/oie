-- name: GetArchiveAlias :one
SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?;
-- name: InsertArchiveAlias :exec
INSERT INTO archive_aliases(account_id,scope,archive_id,message_id) VALUES(?,?,?,?);
-- name: GetArchiveCheckpoint :one
SELECT after_id FROM archive_checkpoints WHERE account_id=? AND scope=?;
-- name: PutArchiveCheckpoint :exec
INSERT INTO archive_checkpoints(account_id,scope,after_id) VALUES(?,?,?)
ON CONFLICT(account_id,scope) DO UPDATE SET after_id=excluded.after_id;
-- name: GetHistoryStatus :one
SELECT payload FROM history_status WHERE account_id=?;
-- name: PutHistoryStatus :exec
INSERT INTO history_status(account_id,payload) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET payload=excluded.payload;
-- name: GetHistoryCounts :one
SELECT CAST((SELECT COUNT(*) FROM messages AS m WHERE m.account_id=sqlc.arg(owner_id) AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)) AS INTEGER) AS retained_messages,
CAST((SELECT COUNT(*) FROM conversations AS c WHERE c.account_id=sqlc.arg(owner_id)) AS INTEGER) AS conversations;

-- name: PutGuardedArchiveCheckpoint :execrows
INSERT INTO archive_checkpoints(account_id,scope,after_id)
SELECT sqlc.arg(account_id),sqlc.arg(scope),sqlc.arg(after_id)
WHERE (SELECT g.archive_revision+COALESCE(a.revision,0)
 FROM global_retention g LEFT JOIN account_archive_retention a ON a.account_id=sqlc.arg(account_id)
 WHERE g.singleton=1)=sqlc.arg(retention_revision)
ON CONFLICT(account_id,scope) DO UPDATE SET after_id=excluded.after_id;

-- name: FindVerifiedRoomOccurrence :many
SELECT DISTINCT r.message_id FROM message_references r
JOIN messages m ON m.id=r.message_id
JOIN conversations c ON c.id=m.conversation_id AND c.account_id=m.account_id
WHERE m.account_id=sqlc.arg(account_id) AND c.address=sqlc.arg(room_address)
  AND json_extract(c.payload,'$.Kind')='room' AND r.scope='room:'||c.address
  AND r.remote_id=sqlc.arg(remote_id) AND instr(r.author,c.address||'/')=1
LIMIT 2;

-- name: IsExcludedXMPPOccurrence :one
SELECT EXISTS (
 SELECT 1 FROM xmpp_message_exclusions e JOIN accounts a ON a.address=e.account AND a.protocol='xmpp'
 WHERE a.id=sqlc.arg(account_id) AND e.chat=sqlc.arg(chat) AND e.kind=sqlc.arg(kind)
 AND ((e.key_type='mam' AND e.authority=sqlc.arg(archive_scope) AND e.remote_id=sqlc.arg(archive_id))
   OR (e.key_type='stanza-id' AND e.authority=e.chat AND e.remote_id=sqlc.arg(stanza_id)))
);

-- name: RememberExcludedXMPPKey :exec
INSERT OR IGNORE INTO xmpp_message_exclusions(account,chat,kind,key_type,authority,remote_id)
SELECT a.address,sqlc.arg(chat),sqlc.arg(kind),sqlc.arg(key_type),sqlc.arg(authority),sqlc.arg(remote_id)
FROM accounts a WHERE a.id=sqlc.arg(account_id) AND a.protocol='xmpp';
