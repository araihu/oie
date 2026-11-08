-- name: GetMessageStar :one
SELECT m.id,m.account_id,m.conversation_id,s.payload FROM messages AS m
LEFT JOIN message_stars AS s ON s.message_id=(
 SELECT candidate.message_id FROM message_stars AS candidate
 WHERE candidate.message_id=m.id OR candidate.message_id IN (
  SELECT linked.alias_id FROM message_canonical_aliases AS linked WHERE linked.canonical_id=m.id
 ) ORDER BY candidate.updated_at DESC,candidate.revision_id DESC LIMIT 1
)
WHERE m.id=COALESCE((SELECT requested.canonical_id FROM message_canonical_aliases AS requested WHERE requested.alias_id=CAST(sqlc.arg(message_id) AS TEXT)),CAST(sqlc.arg(message_id) AS TEXT));

-- name: ListConversationMessageStars :many
SELECT m.id,m.account_id,m.conversation_id,s.payload FROM messages AS m
LEFT JOIN message_stars AS s ON s.message_id=(
 SELECT candidate.message_id FROM message_stars AS candidate
 WHERE candidate.message_id=m.id OR candidate.message_id IN (
  SELECT linked.alias_id FROM message_canonical_aliases AS linked WHERE linked.canonical_id=m.id
 ) ORDER BY candidate.updated_at DESC,candidate.revision_id DESC LIMIT 1
)
WHERE m.conversation_id=sqlc.arg(conversation_id)
AND m.id IN (SELECT COALESCE(a.canonical_id,j.value) FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT)) AS j
LEFT JOIN message_canonical_aliases AS a ON a.alias_id=j.value);

-- name: PutMessageStar :exec
INSERT INTO message_stars(message_id,revision_id,starred,payload,updated_at) VALUES(?,?,?,?,?)
ON CONFLICT(message_id) DO UPDATE SET revision_id=excluded.revision_id,starred=excluded.starred,payload=excluded.payload,updated_at=excluded.updated_at;

-- name: InsertMessageStarRevision :exec
INSERT INTO message_star_revisions(id,message_id,parents,payload,created_at) VALUES(?,?,?,?,?);

-- name: ListStarredMessages :many
SELECT CAST(COALESCE(h.payload,m.payload) AS TEXT) AS message_payload,c.payload AS conversation_payload,s.payload AS star_payload
FROM messages AS m JOIN conversations AS c ON c.id=m.conversation_id
LEFT JOIN message_content_heads AS h ON h.canonical_id=m.id
JOIN message_stars AS s ON s.message_id=(
 SELECT candidate.message_id FROM message_stars AS candidate
 WHERE candidate.message_id=m.id OR candidate.message_id IN (
  SELECT linked.alias_id FROM message_canonical_aliases AS linked WHERE linked.canonical_id=m.id
 ) ORDER BY candidate.updated_at DESC,candidate.revision_id DESC LIMIT 1
)
WHERE s.starred=1
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases AS alias WHERE alias.alias_id=m.id)
AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
AND (CAST(sqlc.arg(page_cursor) AS TEXT)='' OR
 (m.created_at_ms,m.id)<(SELECT x.created_at_ms,x.id FROM messages AS x WHERE x.id=CAST(sqlc.arg(page_cursor) AS TEXT)))
ORDER BY m.created_at_ms DESC,m.id DESC LIMIT sqlc.arg(page_limit);
