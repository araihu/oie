-- name: SearchMessages :many
WITH initial_revisions AS (
 SELECT entity_id,min(rowid) AS revision_row FROM message_revisions GROUP BY entity_id
), roots AS (
 SELECT m.id,m.created_at_ms,m.payload,a.label AS account_label,a.address AS account_address,c.payload AS conversation_payload,
 CAST(COALESCE(h.payload,m.payload) AS TEXT) AS content_payload,
 CAST(COALESCE(head_source.id,first_source.id) AS TEXT) AS matched_revision_id,
 CAST(COALESCE(head_source.created_at,first_source.created_at) AS TEXT) AS matched_at,
 CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=sqlc.arg(parser_version) LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT) AS metadata
 FROM messages m JOIN accounts a ON a.id=m.account_id JOIN conversations c ON c.id=m.conversation_id
 JOIN initial_revisions initial ON initial.entity_id=m.id
 JOIN message_revisions first_source ON first_source.rowid=initial.revision_row
 LEFT JOIN message_content_heads h ON h.canonical_id=m.id
 LEFT JOIN initial_revisions head_initial ON head_initial.entity_id=h.source_id
 LEFT JOIN message_revisions head_source ON head_source.rowid=head_initial.revision_row
 WHERE NOT EXISTS(SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=m.id)
 AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
 AND (CAST(sqlc.arg(account_ids) AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(sqlc.arg(account_ids) AS TEXT))))
 AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
), variants AS (
 SELECT roots.*,CAST(json_extract(content_payload,'$.OriginalText') AS TEXT) AS body,1 AS is_current,'' AS revision_key_at,'' AS revision_key_id FROM roots
 UNION ALL
 SELECT roots.id,roots.created_at_ms,roots.payload,roots.account_label,roots.account_address,roots.conversation_payload,source.payload,source.id,source.created_at,'',CAST(json_extract(source.payload,'$.OriginalText') AS TEXT),0,source.created_at,source.id
 FROM roots JOIN message_content_heads h ON h.canonical_id=roots.id
 JOIN message_correction_facts f ON f.message_id=roots.id OR f.message_id IN (SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=roots.id)
 JOIN message_revisions r ON r.id=f.source_revision_id
 JOIN initial_revisions first ON first.entity_id=f.message_id
 JOIN message_revisions source ON source.rowid=first.revision_row
 WHERE CAST(sqlc.arg(include_history) AS INTEGER)=1 AND source.id<>roots.matched_revision_id
 AND f.source_hash=oie_source_hash(json_extract(r.payload,'$.OriginalText'))
 AND f.source_hash=oie_source_hash(json_extract(source.payload,'$.OriginalText'))
 GROUP BY roots.id,source.id
), matched AS (
 SELECT *,CAST(oie_search_match(body,CAST(sqlc.arg(search) AS TEXT)) AS INTEGER) AS text_match FROM variants
 WHERE oie_search_match(body||' '||metadata,CAST(sqlc.arg(search) AS TEXT))=1
)
SELECT id,created_at_ms,payload,account_label,account_address,conversation_payload,matched_revision_id,matched_at,body,metadata,is_current,revision_key_at,revision_key_id,text_match FROM matched
WHERE (CAST(sqlc.arg(cursor_id) AS TEXT)='' OR (created_at_ms,id,is_current,revision_key_at,revision_key_id)<(CAST(sqlc.arg(cursor_ms) AS INTEGER),CAST(sqlc.arg(cursor_id) AS TEXT),CAST(sqlc.arg(cursor_current) AS INTEGER),CAST(sqlc.arg(cursor_revision_at) AS TEXT),CAST(sqlc.arg(cursor_revision_id) AS TEXT)))
ORDER BY created_at_ms DESC,id DESC,is_current DESC,revision_key_at DESC,revision_key_id DESC LIMIT sqlc.arg(page_limit);

-- name: SearchMessagePreviews :many
WITH candidates AS (
 SELECT sc.id,sc.message_id,sc.url,
 CAST(CASE WHEN json_extract(p.payload,'$.Kind')='image' THEN 'image' ELSE sc.kind END AS TEXT) AS kind,
 CAST(COALESCE(NULLIF(json_extract(p.payload,'$.ImageHash'),''),NULLIF(json_extract(p.payload,'$.FaviconHash'),''),'') AS TEXT) AS image_hash,
 CAST(COALESCE(json_extract(p.payload,'$.FaviconHash'),'') AS TEXT) AS favicon_hash,
 oie_search_match(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),CAST(sqlc.arg(search) AS TEXT)) AS matches_query,
 CASE WHEN NULLIF(json_extract(p.payload,'$.ImageHash'),'') IS NOT NULL THEN 0
      WHEN NULLIF(json_extract(p.payload,'$.FaviconHash'),'') IS NOT NULL THEN 1 ELSE 2 END AS priority
 FROM shared_content AS sc
 JOIN shared_content_state AS s ON s.message_id=sc.message_id AND s.parser_version>=sqlc.arg(parser_version)
 LEFT JOIN message_previews AS p ON p.message_id=sc.message_id AND p.url=sc.url
 WHERE sc.message_id IN (SELECT value FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT)))
), ranked AS (
 SELECT *,row_number() OVER (PARTITION BY message_id ORDER BY matches_query DESC,priority,id) AS position FROM candidates
)
SELECT message_id,url,kind,image_hash,favicon_hash FROM ranked WHERE position=1;
