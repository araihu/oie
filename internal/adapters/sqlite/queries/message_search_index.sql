-- name: GetMessageSearchIndex :one
SELECT * FROM message_search_index WHERE singleton=1;

-- name: StartMessageSearchIndex :exec
INSERT INTO message_search_index(singleton,generation,projection_version,batch,cursor,ready) VALUES(1,sqlc.arg(generation),sqlc.arg(projection_version),sqlc.arg(batch),'',0)
ON CONFLICT(singleton) DO UPDATE SET generation=excluded.generation,projection_version=excluded.projection_version,
 batch=excluded.batch,cursor='',ready=0;

-- name: AdvanceMessageSearchIndex :execrows
UPDATE message_search_index SET batch=sqlc.arg(batch),cursor=sqlc.arg(cursor),ready=sqlc.arg(ready)
WHERE singleton=1 AND generation=sqlc.arg(generation) AND batch=sqlc.arg(previous_batch);

-- name: ClearMessageSearchPending :exec
DELETE FROM message_search_pending;

-- name: ClearMessageSearchExceptions :exec
DELETE FROM message_search_exceptions;

-- name: AcknowledgeMessageSearchPending :exec
DELETE FROM message_search_pending WHERE message_id=sqlc.arg(message_id) AND token=sqlc.arg(token);

-- name: SetMessageSearchException :exec
INSERT OR IGNORE INTO message_search_exceptions(message_id) VALUES(sqlc.arg(message_id));

-- name: RemoveMessageSearchException :exec
DELETE FROM message_search_exceptions WHERE message_id=sqlc.arg(message_id);

-- name: ListMessageSearchPending :many
SELECT * FROM message_search_pending ORDER BY message_id LIMIT sqlc.arg(batch_limit);

-- name: ListMessageSearchUnindexed :many
SELECT message_id FROM message_search_pending
UNION
SELECT message_id FROM message_search_exceptions
ORDER BY message_id
LIMIT sqlc.arg(max_ids);

-- name: ListMessageSearchBuildIDs :many
SELECT id FROM messages WHERE id>sqlc.arg(cursor) ORDER BY id LIMIT sqlc.arg(batch_limit);

-- name: ListMessageSearchDocuments :many
-- This superset includes hidden messages, link metadata awaiting parser
-- reconciliation, and first revisions of aliases before verifying edit facts.
SELECT m.id,m.account_id,m.conversation_id,
 CAST(COALESCE(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText'),'') AS TEXT) AS body,
 CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||
  COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ')
  FROM shared_content sc LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url
  WHERE sc.message_id=m.id),'') AS TEXT) AS metadata,
 CAST(COALESCE((SELECT group_concat(json_extract(r.payload,'$.OriginalText'),' ')
  FROM (
   SELECT m.id AS id UNION SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=m.id
  ) member JOIN message_revisions r ON r.rowid=(SELECT min(first.rowid) FROM message_revisions first WHERE first.entity_id=member.id)
 ),'') AS TEXT) AS history
FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT)) x CROSS JOIN messages m ON m.id=x.value
LEFT JOIN message_content_heads h ON h.canonical_id=m.id;

-- name: CountMessageSearchRoomPrefix :one
SELECT COUNT(*) FROM (SELECT 1 FROM messages WHERE conversation_id=sqlc.arg(conversation_id) LIMIT sqlc.arg(max_count));

-- name: VerifyIndexedMessages :many
WITH candidate_ids AS (
 SELECT DISTINCT m.id FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT)) x CROSS JOIN messages m ON m.id=x.value
), roots AS (
 SELECT m.id,CAST(COALESCE(m.timeline_at_ms,m.created_at_ms) AS INTEGER) AS position_ms,m.timeline_at_ms,m.payload,a.label AS account_label,a.address AS account_address,c.payload AS conversation_payload,
 CAST(COALESCE(h.payload,m.payload) AS TEXT) AS content_payload,
 CAST(COALESCE(head_source.id,first_source.id) AS TEXT) AS matched_revision_id,
 CAST(COALESCE(head_source.created_at,first_source.created_at) AS TEXT) AS matched_at,
 CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=sqlc.arg(parser_version) LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT) AS metadata
 FROM candidate_ids x CROSS JOIN messages m ON m.id=x.id JOIN accounts a ON a.id=m.account_id JOIN conversations c ON c.id=m.conversation_id
 JOIN message_revisions first_source ON first_source.rowid=(SELECT min(r.rowid) FROM message_revisions r WHERE r.entity_id=m.id)
 LEFT JOIN message_content_heads h ON h.canonical_id=m.id
 LEFT JOIN message_revisions head_source ON head_source.rowid=(SELECT min(r.rowid) FROM message_revisions r WHERE r.entity_id=h.source_id)
 WHERE NOT EXISTS(SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=m.id)
 AND json_extract(c.payload,'$.Request') IS NULL
 AND NOT EXISTS (SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=m.id)
 AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
 AND (CAST(sqlc.arg(account_ids) AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(sqlc.arg(account_ids) AS TEXT))))
 AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
), variants AS (
 SELECT roots.*,CAST(json_extract(content_payload,'$.OriginalText') AS TEXT) AS body,1 AS is_current,'' AS revision_key_at,'' AS revision_key_id FROM roots
 UNION ALL
 SELECT roots.id,roots.position_ms,roots.timeline_at_ms,roots.payload,roots.account_label,roots.account_address,roots.conversation_payload,source.payload,source.id,source.created_at,'',CAST(json_extract(source.payload,'$.OriginalText') AS TEXT),0,source.created_at,source.id
 FROM roots JOIN message_content_heads h ON h.canonical_id=roots.id
 JOIN message_correction_facts f ON f.message_id=roots.id OR f.message_id IN (SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=roots.id)
 JOIN message_revisions r ON r.id=f.source_revision_id
 JOIN message_revisions source ON source.rowid=(SELECT min(r.rowid) FROM message_revisions r WHERE r.entity_id=f.message_id)
 WHERE CAST(sqlc.arg(include_history) AS INTEGER)=1 AND source.id<>roots.matched_revision_id
 AND f.source_hash=oie_source_hash(json_extract(r.payload,'$.OriginalText'))
 AND f.source_hash=oie_source_hash(json_extract(source.payload,'$.OriginalText'))
 GROUP BY roots.id,source.id
), matched AS (
 SELECT *,CAST(oie_search_match(body,CAST(sqlc.arg(search) AS TEXT)) AS INTEGER) AS text_match FROM variants
 WHERE oie_search_match(body||' '||metadata,CAST(sqlc.arg(search) AS TEXT))=1
)
SELECT id,position_ms,timeline_at_ms,payload,account_label,account_address,conversation_payload,matched_revision_id,matched_at,body,metadata,is_current,revision_key_at,revision_key_id,text_match FROM matched
WHERE (CAST(sqlc.arg(cursor_id) AS TEXT)='' OR (position_ms,id,is_current,revision_key_at,revision_key_id)<(CAST(sqlc.arg(cursor_ms) AS INTEGER),CAST(sqlc.arg(cursor_id) AS TEXT),CAST(sqlc.arg(cursor_current) AS INTEGER),CAST(sqlc.arg(cursor_revision_at) AS TEXT),CAST(sqlc.arg(cursor_revision_id) AS TEXT)))
ORDER BY position_ms DESC,id DESC,is_current DESC,revision_key_at DESC,revision_key_id DESC LIMIT sqlc.arg(page_limit);

-- name: SearchMessagesChronological :many
WITH page AS (
 SELECT m.id,CAST(COALESCE(m.timeline_at_ms,m.created_at_ms) AS INTEGER) AS position_ms,m.timeline_at_ms,m.payload,m.account_id,m.conversation_id,h.source_id,
 CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT) AS body, CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=sqlc.arg(parser_version) LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT) AS metadata
 FROM messages m INDEXED BY messages_global_search_timeline LEFT JOIN message_content_heads h ON h.canonical_id=m.id
 WHERE EXISTS(SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
 AND EXISTS(SELECT 1 FROM message_revisions r WHERE r.entity_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM hidden_scheduled_messages x WHERE x.message_id=m.id)
 AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
 AND (CAST(sqlc.arg(account_ids) AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(sqlc.arg(account_ids) AS TEXT))))
 AND (CAST(sqlc.arg(conversation_id) AS TEXT)='' OR m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT))
 AND COALESCE(m.timeline_at_ms,m.created_at_ms) <= sqlc.arg(cursor_ms)
 AND (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id,1,'','')<(CAST(sqlc.arg(cursor_ms) AS INTEGER),CAST(sqlc.arg(cursor_id) AS TEXT),CAST(sqlc.arg(cursor_current) AS INTEGER),CAST(sqlc.arg(cursor_revision_at) AS TEXT),CAST(sqlc.arg(cursor_revision_id) AS TEXT))
 AND oie_search_match(CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT)||' '||CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=sqlc.arg(parser_version) LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT),CAST(sqlc.arg(search) AS TEXT))=1
 ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT sqlc.arg(page_limit)
)
SELECT page.id,page.position_ms,page.timeline_at_ms,page.payload,a.label AS account_label,a.address AS account_address,c.payload AS conversation_payload,
 CAST(COALESCE(head_source.id,first_source.id) AS TEXT) AS matched_revision_id,
 CAST(COALESCE(head_source.created_at,first_source.created_at) AS TEXT) AS matched_at,
 page.body,page.metadata,CAST(1 AS INTEGER) AS is_current,CAST('' AS TEXT) AS revision_key_at,CAST('' AS TEXT) AS revision_key_id,
 CAST(oie_search_match(page.body,CAST(sqlc.arg(search) AS TEXT)) AS INTEGER) AS text_match
 FROM page JOIN accounts a ON a.id=page.account_id JOIN conversations c ON c.id=page.conversation_id
 JOIN message_revisions first_source ON first_source.rowid=(SELECT min(r.rowid) FROM message_revisions r WHERE r.entity_id=page.id)
 LEFT JOIN message_revisions head_source ON head_source.rowid=(SELECT min(r.rowid) FROM message_revisions r WHERE r.entity_id=page.source_id)
 ORDER BY page.position_ms DESC,page.id DESC;

-- name: SearchRoomMessagesChronological :many
WITH page AS (
 SELECT m.id,CAST(COALESCE(m.timeline_at_ms,m.created_at_ms) AS INTEGER) AS position_ms,m.timeline_at_ms,m.payload,m.account_id,m.conversation_id,h.source_id,
 CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT) AS body, CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=sqlc.arg(parser_version) LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT) AS metadata
 FROM messages m INDEXED BY messages_timeline LEFT JOIN message_content_heads h ON h.canonical_id=m.id
 WHERE EXISTS(SELECT 1 FROM conversations c WHERE c.id=m.conversation_id AND json_extract(c.payload,'$.Request') IS NULL)
 AND EXISTS(SELECT 1 FROM message_revisions r WHERE r.entity_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM hidden_scheduled_messages x WHERE x.message_id=m.id)
 AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR m.account_id=CAST(sqlc.arg(account_id) AS TEXT))
 AND (CAST(sqlc.arg(account_ids) AS TEXT)='[]' OR m.account_id IN (SELECT value FROM json_each(CAST(sqlc.arg(account_ids) AS TEXT))))
 AND m.conversation_id=CAST(sqlc.arg(conversation_id) AS TEXT)
 AND COALESCE(m.timeline_at_ms,m.created_at_ms) <= sqlc.arg(cursor_ms)
 AND (COALESCE(m.timeline_at_ms,m.created_at_ms),m.id,1,'','')<(CAST(sqlc.arg(cursor_ms) AS INTEGER),CAST(sqlc.arg(cursor_id) AS TEXT),CAST(sqlc.arg(cursor_current) AS INTEGER),CAST(sqlc.arg(cursor_revision_at) AS TEXT),CAST(sqlc.arg(cursor_revision_id) AS TEXT))
 AND oie_search_match(CAST(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText') AS TEXT)||' '||CAST(COALESCE((SELECT group_concat(sc.url||' '||sc.file_name||' '||COALESCE(json_extract(p.payload,'$.Title'),'')||' '||COALESCE(json_extract(p.payload,'$.Description'),''),' ') FROM shared_content sc JOIN shared_content_state state ON state.message_id=sc.message_id AND state.parser_version>=sqlc.arg(parser_version) LEFT JOIN message_previews p ON p.message_id=sc.message_id AND p.url=sc.url WHERE sc.message_id=m.id),'') AS TEXT),CAST(sqlc.arg(search) AS TEXT))=1
 ORDER BY COALESCE(m.timeline_at_ms,m.created_at_ms) DESC,m.id DESC LIMIT sqlc.arg(page_limit)
)
SELECT page.id,page.position_ms,page.timeline_at_ms,page.payload,a.label AS account_label,a.address AS account_address,c.payload AS conversation_payload,
 CAST(COALESCE(head_source.id,first_source.id) AS TEXT) AS matched_revision_id,
 CAST(COALESCE(head_source.created_at,first_source.created_at) AS TEXT) AS matched_at,
 page.body,page.metadata,CAST(1 AS INTEGER) AS is_current,CAST('' AS TEXT) AS revision_key_at,CAST('' AS TEXT) AS revision_key_id,
 CAST(oie_search_match(page.body,CAST(sqlc.arg(search) AS TEXT)) AS INTEGER) AS text_match
 FROM page JOIN accounts a ON a.id=page.account_id JOIN conversations c ON c.id=page.conversation_id
 JOIN message_revisions first_source ON first_source.rowid=(SELECT min(r.rowid) FROM message_revisions r WHERE r.entity_id=page.id)
 LEFT JOIN message_revisions head_source ON head_source.rowid=(SELECT min(r.rowid) FROM message_revisions r WHERE r.entity_id=page.source_id)
 ORDER BY page.position_ms DESC,page.id DESC;
