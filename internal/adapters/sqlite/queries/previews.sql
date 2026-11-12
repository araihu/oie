-- name: ListPreviewSources :many
SELECT CAST(COALESCE(m.id,'') AS TEXT) AS message_id,
 CAST(COALESCE(m.account_id,'') AS TEXT) AS account_id,
 CAST(COALESCE(m.conversation_id,'') AS TEXT) AS conversation_id,
 CAST(COALESCE(h.payload,m.payload,'') AS TEXT) AS message_payload,
 r.payload AS reply_payload
FROM json_each(CAST(sqlc.arg(message_ids) AS TEXT)) AS requested
LEFT JOIN message_canonical_aliases AS a ON a.alias_id=requested.value
LEFT JOIN messages AS m ON m.id=COALESCE(a.canonical_id,requested.value)
LEFT JOIN message_content_heads AS h ON h.canonical_id=m.id
LEFT JOIN message_replies AS r ON r.message_id=COALESCE(h.source_id,m.id)
 AND r.body_hash=oie_source_hash(json_extract(COALESCE(h.payload,m.payload),'$.OriginalText'))
ORDER BY CAST(requested.key AS INTEGER);

-- name: GetMessagePreview :one
SELECT * FROM message_previews WHERE id=?;
-- name: FindMessagePreview :one
SELECT * FROM message_previews WHERE message_id=? AND url=?;
-- name: InsertMessagePreview :execrows
INSERT INTO message_previews(id,revision_id,account_id,conversation_id,message_id,url,state,download_state,updated_at,payload)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(message_id,url) DO NOTHING;
-- name: UpdateMessagePreview :execrows
UPDATE message_previews SET revision_id=?,state=?,download_state=?,updated_at=?,payload=? WHERE id=? AND revision_id=?;
-- name: InsertPreviewRevision :exec
INSERT INTO preview_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?);
-- name: PreviewJobs :many
SELECT * FROM message_previews WHERE state='queued' OR download_state='queued' OR (state='unavailable' AND coalesce(json_extract(payload,'$.Kind'),'')<>'video' AND updated_at<sqlc.arg(retry_before))
ORDER BY CASE WHEN download_state='queued' THEN 0 ELSE 1 END,updated_at,id LIMIT sqlc.arg(job_limit);
-- name: InterruptedPreviewJobs :many
SELECT * FROM message_previews WHERE download_state='downloading' ORDER BY id LIMIT ?;
-- name: InsertMediaFile :exec
INSERT INTO media_files(hash,mime_type,size,created_at) VALUES(?,?,?,?) ON CONFLICT(hash) DO NOTHING;
-- name: GetMediaFile :one
SELECT * FROM media_files WHERE hash=?;
