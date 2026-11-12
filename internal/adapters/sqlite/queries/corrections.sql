-- name: InsertCorrectionFact :execrows
INSERT INTO message_correction_facts(message_id,scope,author,remote_id,target_id,source_hash,source_revision_id) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING;

-- name: CorrectionNeighbors :many
SELECT f.*,m.payload AS message_payload FROM message_correction_facts f JOIN messages m ON m.id=f.message_id
WHERE m.account_id=sqlc.arg(account_id) AND m.conversation_id=sqlc.arg(conversation_id)
AND f.scope=sqlc.arg(scope) AND f.author=sqlc.arg(author)
AND (f.remote_id IN (SELECT value FROM json_each(CAST(sqlc.arg(remote_ids) AS TEXT)))
OR f.target_id IN (SELECT value FROM json_each(CAST(sqlc.arg(remote_ids) AS TEXT))))
ORDER BY f.message_id LIMIT 129;

-- name: GetCorrectionFacts :many
SELECT * FROM message_correction_facts WHERE message_id=? LIMIT 129;

-- name: ResolveMessageAlias :one
SELECT canonical_id FROM message_canonical_aliases WHERE alias_id=?;

-- name: GetContentHead :one
SELECT * FROM message_content_heads WHERE canonical_id=?;

-- name: PutContentHead :exec
INSERT INTO message_content_heads(canonical_id,source_id,revision_id,payload,edited_at) VALUES(?,?,?,?,?)
ON CONFLICT(canonical_id) DO UPDATE SET source_id=excluded.source_id,revision_id=excluded.revision_id,payload=excluded.payload,edited_at=excluded.edited_at;

-- name: PutCanonicalAlias :exec
INSERT INTO message_canonical_aliases(alias_id,canonical_id) VALUES(?,?) ON CONFLICT(alias_id) DO UPDATE SET canonical_id=excluded.canonical_id;

-- name: ClearCanonicalAliases :exec
DELETE FROM message_canonical_aliases WHERE alias_id IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT))) OR canonical_id IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT)));

-- name: ClearContentHeads :exec
DELETE FROM message_content_heads WHERE canonical_id IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT)));

-- name: LatestLogicalMessage :one
SELECT m.* FROM messages m WHERE m.conversation_id=?
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT 1;

-- name: ListCanonicalMessageReferences :many
SELECT r.scope,r.remote_id,r.author,r.source_hash,m.payload AS message_payload
FROM message_references r JOIN messages m ON m.id=r.message_id
LEFT JOIN message_canonical_aliases a ON a.alias_id=m.id
WHERE (r.message_id=sqlc.arg(canonical_id) OR r.message_id IN
 (SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=sqlc.arg(canonical_id)))
AND NOT EXISTS(
 SELECT 1 FROM message_references conflict INDEXED BY message_references_lookup CROSS JOIN messages other ON other.id=conflict.message_id
 LEFT JOIN message_canonical_aliases other_alias ON other_alias.alias_id=other.id
 WHERE other.account_id=m.account_id AND other.conversation_id=m.conversation_id
 AND conflict.scope=r.scope AND conflict.remote_id=r.remote_id
 AND coalesce(other_alias.canonical_id,other.id)<>sqlc.arg(canonical_id)
)
AND NOT EXISTS (
 SELECT 1 FROM message_references tainted INDEXED BY message_references_lookup CROSS JOIN messages observed ON observed.id=tainted.message_id
 WHERE observed.account_id=m.account_id AND observed.conversation_id=m.conversation_id
 AND tainted.scope=r.scope AND tainted.remote_id=r.remote_id
 AND tainted.source_hash<>oie_source_hash(json_extract(observed.payload,'$.OriginalText'))
)
ORDER BY (a.alias_id IS NOT NULL),r.scope,r.remote_id,r.author LIMIT 64;
