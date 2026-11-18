-- Allocate a local catalog identity only when a verified exact alias can
-- fill it. The normal catalog indexer decides visibility and reuses its ID.
WITH candidates AS (
  SELECT target.message_id,source.url,source.kind,source.file_name,
         row_number() OVER (PARTITION BY target.message_id,source.url ORDER BY source.id) AS priority
  FROM backup.shared_content source
  JOIN restore_media_targets target ON target.id=source.message_id
  JOIN restore_eligible_downloads f ON f.source_id=source.id AND f.source_revision=target.source_revision
    AND f.kind='file' AND f.has_bytes=1
  WHERE target.message_id<>source.message_id AND target.accepts_files=1
    AND oie_quote_preview_allowed(target.payload,source.url)
    AND NOT EXISTS (SELECT 1 FROM shared_content_state catalog WHERE catalog.message_id=target.message_id)
    AND NOT EXISTS (SELECT 1 FROM shared_content local WHERE local.message_id=target.message_id AND local.url=source.url)
)
SELECT message_id,url,kind,file_name FROM candidates WHERE priority=1;
