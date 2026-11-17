-- name: UnredactedQuoteBodies :many
SELECT DISTINCT CAST(json_extract(payload,'$.OriginalText') AS TEXT) AS body
FROM message_revisions
WHERE entity_id=sqlc.arg(message_id)
  AND oie_source_hash(json_extract(payload,'$.OriginalText'))=sqlc.arg(body_hash);

-- name: RedactedQuotePreviewIDs :many
SELECT id FROM message_previews WHERE message_id=sqlc.arg(message_id)
AND url IN (SELECT value FROM json_each(CAST(sqlc.arg(urls) AS TEXT)));

-- name: DeleteRedactedQuotePreviewEvents :exec
DELETE FROM changes WHERE kind='message.preview_changed'
AND json_extract(payload,'$.ID') IN (SELECT value FROM json_each(CAST(sqlc.arg(preview_ids) AS TEXT)));

-- name: DeleteRedactedQuotePreviewRevisions :exec
DELETE FROM preview_revisions
WHERE entity_id IN (SELECT value FROM json_each(CAST(sqlc.arg(preview_ids) AS TEXT)));

-- name: DeleteRedactedQuotePreviews :exec
DELETE FROM message_previews
WHERE id IN (SELECT value FROM json_each(CAST(sqlc.arg(preview_ids) AS TEXT)));

-- name: DeleteRedactedQuoteSharedContent :exec
DELETE FROM shared_content WHERE message_id=sqlc.arg(message_id)
AND url IN (SELECT value FROM json_each(CAST(sqlc.arg(urls) AS TEXT)));
