-- name: ListMessageHistorySources :many
WITH source_ids AS (
 SELECT CAST(sqlc.arg(canonical_id) AS TEXT) AS id
 UNION SELECT alias_id AS id FROM message_canonical_aliases WHERE canonical_id=sqlc.arg(canonical_id)
 LIMIT 129
)
SELECT m.id AS source_id,r.id AS revision_id,r.payload,r.created_at
FROM source_ids ids CROSS JOIN messages m ON m.id=ids.id
JOIN message_revisions r ON r.rowid=(
 SELECT min(first.rowid) FROM message_revisions first WHERE first.entity_id=m.id
)
WHERE m.account_id=sqlc.arg(account_id) AND m.conversation_id=sqlc.arg(conversation_id)
ORDER BY m.id LIMIT 129;

-- name: ListMessageHistoryProofs :many
WITH source_ids AS (
 SELECT CAST(sqlc.arg(canonical_id) AS TEXT) AS id
 UNION SELECT alias_id AS id FROM message_canonical_aliases WHERE canonical_id=sqlc.arg(canonical_id)
 LIMIT 129
)
SELECT f.*,r.payload AS proof_payload
FROM source_ids ids CROSS JOIN messages m ON m.id=ids.id
CROSS JOIN message_correction_facts f ON f.message_id=m.id
LEFT JOIN message_revisions r ON r.id=f.source_revision_id AND r.entity_id=m.id
WHERE m.account_id=sqlc.arg(account_id) AND m.conversation_id=sqlc.arg(conversation_id)
ORDER BY f.message_id,f.scope,f.author,f.remote_id,f.target_id,f.source_hash LIMIT 129;
