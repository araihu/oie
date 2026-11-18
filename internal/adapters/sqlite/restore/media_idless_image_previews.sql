-- Create a local preview only when the archive can actually fill it. Keep
-- the archived preview's ID and revisions attached to their original source.
WITH candidates AS (
  SELECT target.message_id,target.account_id,target.conversation_id,p.payload,p.url,
         row_number() OVER (PARTITION BY target.message_id,p.url ORDER BY p.updated_at DESC,p.id DESC) AS priority
  FROM backup.message_previews p JOIN restore_image_messages target ON target.id=p.message_id
  WHERE target.message_id<>p.message_id AND p.account_id=target.account_id AND p.conversation_id=target.conversation_id
    AND p.download_state='ready' AND json_extract(p.payload,'$.Kind')='image'
    AND oie_quote_preview_allowed(target.payload,p.url)
    AND NOT EXISTS (SELECT 1 FROM message_previews local WHERE local.message_id=target.message_id AND local.url=p.url)
    AND EXISTS (
      SELECT 1 FROM restore_eligible_downloads f
      WHERE f.kind='image' AND f.source_id=p.id AND f.has_bytes=1
        AND f.hash=json_extract(p.payload,'$.FileHash') AND f.size=json_extract(p.payload,'$.FileSize')
    )
)
SELECT message_id,account_id,conversation_id,url,payload FROM candidates WHERE priority=1;
