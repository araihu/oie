-- name: InsertMessageRetraction :execrows
INSERT OR IGNORE INTO message_retractions(account_id,kind,address,scope,remote_id,retracted_at)
VALUES (?,?,?,?,?,?);

-- name: GetMessageRetraction :one
SELECT retracted_at
FROM retained_message_retractions WHERE EXISTS (SELECT 1 FROM message_retractions)
AND source_id IN (SELECT sqlc.arg(message_id) AS source_id UNION ALL SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=sqlc.arg(message_id)) ORDER BY retracted_at LIMIT 1;

-- name: MessageRetractionSources :many
SELECT source_id FROM message_retraction_targets
WHERE account_id=sqlc.arg(account_id) AND kind=sqlc.arg(kind) AND address=sqlc.arg(address)
AND scope=sqlc.arg(scope) AND remote_id=sqlc.arg(remote_id);
