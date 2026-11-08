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
SELECT CAST((SELECT COUNT(*) FROM messages AS m WHERE m.account_id=sqlc.arg(owner_id)) AS INTEGER) AS retained_messages,
CAST((SELECT COUNT(*) FROM conversations AS c WHERE c.account_id=sqlc.arg(owner_id)) AS INTEGER) AS conversations;
