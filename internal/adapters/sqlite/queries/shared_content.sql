-- name: SharedContentCandidates :many
SELECT m.* FROM messages AS m LEFT JOIN shared_content_state AS s ON s.message_id=m.id
WHERE (s.message_id IS NULL OR s.parser_version<sqlc.arg(parser_version))
AND EXISTS (SELECT 1 FROM conversations visible WHERE visible.id=m.conversation_id AND json_extract(visible.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases AS alias WHERE alias.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT sqlc.arg(batch_limit);

-- name: PutSharedContent :exec
INSERT INTO shared_content(id,revision_id,message_id,url,kind,file_name) VALUES(?,?,?,?,?,?)
ON CONFLICT(message_id,url) DO UPDATE SET revision_id=excluded.revision_id,kind=excluded.kind,file_name=excluded.file_name
WHERE shared_content.kind<>excluded.kind OR shared_content.file_name<>excluded.file_name;

-- name: DeleteObsoleteSharedContent :exec
DELETE FROM shared_content WHERE message_id=sqlc.arg(message_id)
AND url NOT IN (SELECT value FROM json_each(CAST(sqlc.arg(current_urls) AS TEXT)));

-- name: MarkSharedContentIndexed :exec
INSERT INTO shared_content_state(message_id,parser_version,caption) VALUES(?,?,?)
ON CONFLICT(message_id) DO UPDATE SET parser_version=excluded.parser_version,caption=excluded.caption;

-- name: InvalidateSharedContent :exec
DELETE FROM shared_content_state WHERE message_id=?;

-- name: SharedContentIndexStatus :one
SELECT COUNT(*) AS total_messages,COUNT(CASE WHEN s.parser_version>=sqlc.arg(parser_version) THEN 1 END) AS indexed_messages
FROM messages AS m LEFT JOIN shared_content_state AS s ON s.message_id=m.id
WHERE (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
AND (CAST(sqlc.arg(account_ids) AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(sqlc.arg(account_ids) AS TEXT))))
AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
AND (CAST(sqlc.arg(message_ids) AS TEXT)='[]' OR m.id IN (SELECT value FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT))))
AND EXISTS (SELECT 1 FROM conversations visible WHERE visible.id=m.conversation_id AND json_extract(visible.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases AS alias WHERE alias.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id);

-- name: ListSharedContent :many
SELECT sc.id,sc.revision_id,CAST(CASE WHEN json_extract(p.payload,'$.Kind')='image' THEN 'image' ELSE sc.kind END AS TEXT) AS kind,sc.url,sc.file_name,CAST(COALESCE(h.payload,m.payload) AS TEXT) AS message_payload,m.timeline_at_ms,c.payload AS conversation_payload,a.label AS account_label,p.payload AS preview_payload
FROM shared_content AS sc JOIN messages AS m ON m.id=sc.message_id
LEFT JOIN message_content_heads AS h ON h.canonical_id=m.id
JOIN shared_content_state AS s ON s.message_id=m.id AND s.parser_version>=sqlc.arg(parser_version)
JOIN conversations AS c ON c.id=m.conversation_id JOIN accounts AS a ON a.id=m.account_id
LEFT JOIN message_previews AS p ON p.message_id=m.id AND p.url=sc.url
WHERE (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
AND (CAST(sqlc.arg(account_ids) AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(sqlc.arg(account_ids) AS TEXT))))
AND EXISTS (SELECT 1 FROM conversations visible WHERE visible.id=m.conversation_id AND json_extract(visible.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases AS alias WHERE alias.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages hidden WHERE hidden.message_id=m.id)
AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
AND (CAST(sqlc.arg(message_ids) AS TEXT)='[]' OR m.id IN (SELECT value FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT))))
AND ((CAST(sqlc.arg(content_type) AS TEXT)='media' AND (sc.kind IN ('image','video','audio') OR json_extract(p.payload,'$.Kind')='image'))
 OR (CAST(sqlc.arg(content_type) AS TEXT)='video' AND sc.kind='video' AND coalesce(json_extract(p.payload,'$.Kind'),'')<>'image')
 OR (CAST(sqlc.arg(content_type) AS TEXT)='links' AND sc.kind='link' AND coalesce(json_extract(p.payload,'$.Kind'),'')<>'image')
 OR (CAST(sqlc.arg(content_type) AS TEXT)='documents' AND sc.kind='document' AND coalesce(json_extract(p.payload,'$.Kind'),'')<>'image'))
AND (CAST(sqlc.arg(cursor_id) AS TEXT)='' OR (COALESCE(m.timeline_at_ms,m.created_at_ms),sc.id)<(CAST(sqlc.arg(cursor_ms) AS INTEGER),CAST(sqlc.arg(cursor_id) AS TEXT)))
AND (CAST(sqlc.arg(search) AS TEXT)='' OR oie_search_match(sc.url||' '||sc.file_name||' '||s.caption||' '||coalesce(json_extract(m.payload,'$.Sender.DisplayName'),'')||' '||coalesce(json_extract(c.payload,'$.Title'),'')||' '||a.label||' '||coalesce(json_extract(p.payload,'$.Title'),'')||' '||coalesce(json_extract(p.payload,'$.Description'),''),CAST(sqlc.arg(search) AS TEXT))=1)
ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,sc.id DESC LIMIT sqlc.arg(page_limit);

-- name: GetSharedContentItem :one
SELECT sc.id,sc.revision_id,sc.message_id,sc.url,CAST(CASE WHEN json_extract(p.payload,'$.Kind')='image' THEN 'image' ELSE sc.kind END AS TEXT) AS kind,sc.file_name,CAST(COALESCE(h.payload,m.payload) AS TEXT) AS message_payload,m.timeline_at_ms
FROM shared_content sc JOIN messages m ON m.id=sc.message_id
LEFT JOIN message_previews p ON p.message_id=m.id AND p.url=sc.url
LEFT JOIN message_content_heads h ON h.canonical_id=m.id
JOIN shared_content_state s ON s.message_id=m.id AND s.parser_version>=sqlc.arg(parser_version)
JOIN conversations c ON c.id=m.conversation_id JOIN accounts a ON a.id=m.account_id
WHERE sc.id=sqlc.arg(item_id)
AND EXISTS (SELECT 1 FROM conversations visible WHERE visible.id=m.conversation_id AND json_extract(visible.payload,'$.Request') IS NULL)
AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=m.id)
AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages hidden WHERE hidden.message_id=m.id);
