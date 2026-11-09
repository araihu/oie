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
