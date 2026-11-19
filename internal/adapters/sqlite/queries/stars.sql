-- name: GetMessageStar :one
SELECT m.id,m.account_id,m.conversation_id,s.payload FROM messages AS m
LEFT JOIN message_stars AS s ON s.message_id=(
 SELECT candidate.message_id FROM message_stars AS candidate
 WHERE candidate.message_id=m.id OR candidate.message_id IN (
  SELECT linked.alias_id FROM message_canonical_aliases AS linked WHERE linked.canonical_id=m.id
 ) ORDER BY candidate.updated_at DESC,candidate.revision_id DESC LIMIT 1
)
WHERE m.id=COALESCE((SELECT requested.canonical_id FROM message_canonical_aliases AS requested WHERE requested.alias_id=CAST(sqlc.arg(message_id) AS TEXT)),CAST(sqlc.arg(message_id) AS TEXT));

-- name: ListMessageStars :many
WITH requested AS (
 SELECT DISTINCT COALESCE(a.canonical_id,CAST(j.value AS TEXT)) AS id
 FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT)) AS j
 LEFT JOIN message_canonical_aliases AS a ON a.alias_id=j.value
)
SELECT CAST(COALESCE(m.id,'') AS TEXT) AS id,
 CAST(COALESCE(m.account_id,'') AS TEXT) AS account_id,
 CAST(COALESCE(m.conversation_id,'') AS TEXT) AS conversation_id,s.payload
FROM requested
LEFT JOIN messages AS m ON m.id=requested.id
 AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
LEFT JOIN message_stars AS s ON s.message_id=(
 SELECT candidate.message_id FROM message_stars AS candidate
 WHERE candidate.message_id=m.id OR candidate.message_id IN (
  SELECT linked.alias_id FROM message_canonical_aliases AS linked WHERE linked.canonical_id=m.id
 ) ORDER BY candidate.updated_at DESC,candidate.revision_id DESC LIMIT 1
);

-- name: PutMessageStar :exec
INSERT INTO message_stars(message_id,revision_id,starred,payload,updated_at) VALUES(?,?,?,?,?)
ON CONFLICT(message_id) DO UPDATE SET revision_id=excluded.revision_id,starred=excluded.starred,payload=excluded.payload,updated_at=excluded.updated_at;

-- name: RetireInheritedIdlessStarHeads :exec
-- A local star decision supersedes inherited heads without erasing their
-- immutable revisions, even when the inherited clock was ahead.
DELETE FROM message_stars WHERE message_id IN (
 SELECT alias.id FROM message_canonical_aliases link
 JOIN messages root ON root.id=link.canonical_id AND root.is_own=1 AND root.state='submitted'
 JOIN messages alias ON alias.id=link.alias_id AND alias.account_id=root.account_id
  AND alias.conversation_id=root.conversation_id AND alias.is_own=1 AND alias.state='submitted' AND alias.provider_id=''
 JOIN conversations chat ON chat.id=root.conversation_id AND json_extract(chat.payload,'$.Kind')='direct'
 WHERE root.id=sqlc.arg(message_id)
  AND root.id<>json_extract(root.payload,'$.OwnOriginID')
  AND json_extract(root.payload,'$.OwnOriginID')<>''
  AND json_extract(alias.payload,'$.OwnOriginID')=json_extract(root.payload,'$.OwnOriginID')
 UNION ALL
 SELECT alias.id FROM message_canonical_aliases link
 JOIN messages root ON root.id=link.canonical_id AND root.is_own=1 AND root.state='submitted'
 JOIN messages alias ON alias.id=link.alias_id AND alias.account_id=root.account_id
  AND alias.conversation_id=root.conversation_id AND alias.is_own=1 AND alias.state='submitted' AND alias.provider_id=''
 JOIN conversations chat ON chat.id=root.conversation_id AND json_extract(chat.payload,'$.Kind')='room'
 JOIN message_revisions original ON original.id=alias.revision_id AND original.entity_id=alias.id
 WHERE root.id=sqlc.arg(message_id) AND root.provider_id LIKE 'room:%'
   AND json_extract(original.payload,'$.ProviderID')=root.provider_id
   AND NOT EXISTS(SELECT 1 FROM message_correction_facts f WHERE f.message_id=alias.id)
 UNION ALL
 SELECT echo_id FROM room_archive_echo_links WHERE original_id=sqlc.arg(message_id)
);

-- name: InsertMessageStarRevision :exec
INSERT INTO message_star_revisions(id,message_id,parents,payload,created_at) VALUES(?,?,?,?,?);

-- name: ListStarredMessages :many
SELECT CAST(COALESCE(h.payload,m.payload) AS TEXT) AS message_payload,m.timeline_at_ms,c.payload AS conversation_payload,s.payload AS star_payload
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
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages hidden WHERE hidden.message_id=m.id)
AND (CAST(sqlc.arg(search) AS TEXT)='' OR oie_search_match(
 CASE WHEN EXISTS (
 SELECT 1 FROM shared_catalog_moderated_sources x WHERE x.message_id=m.id
) OR EXISTS (
 SELECT 1 FROM retained_message_retractions r
 WHERE r.source_id IN (SELECT m.id UNION ALL SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=m.id)
) THEN '' ELSE COALESCE(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText'),'') END || ' ' ||
 COALESCE(json_extract(m.payload,'$.Sender.DisplayName'),'') || ' ' ||
 COALESCE(json_extract(c.payload,'$.Title'),'') || ' ' || c.address,
 CAST(sqlc.arg(search) AS TEXT))=1)
AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
AND (CAST(sqlc.arg(page_cursor) AS TEXT)='' OR
 (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id)<(SELECT COALESCE(x.timeline_at_ms,x.created_at_ms),x.id FROM messages AS x WHERE x.id=CAST(sqlc.arg(page_cursor) AS TEXT)))
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT sqlc.arg(page_limit);
