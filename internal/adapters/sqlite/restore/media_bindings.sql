CREATE TEMP TABLE restore_downloads AS
WITH resolved_links AS MATERIALIZED (
    SELECT DISTINCT entity_id FROM preview_revisions
    WHERE json_extract(payload,'$.Kind')='link' AND json_extract(payload,'$.State')='ready'
)
SELECT f.device_id, local.id AS source_id, f.source_revision, f.kind, f.hash, f.mime_type,
       f.size, f.verified_algorithm, f.downloaded_at_ms, f.cleanup_revision_id
FROM restore_eligible_downloads f
JOIN backup.shared_content source ON source.id=f.source_id
JOIN restore_matching_media_messages m ON m.id=source.message_id AND m.revision_id=f.source_revision
JOIN shared_content local ON local.message_id=source.message_id AND local.url=source.url AND local.kind=source.kind
WHERE f.kind='file' AND f.has_bytes=1 AND oie_quote_preview_allowed(m.payload,source.url)
UNION ALL
SELECT f.device_id, COALESCE(local.id,source.id), f.source_revision, f.kind, f.hash, f.mime_type,
       f.size, f.verified_algorithm, f.downloaded_at_ms, f.cleanup_revision_id
FROM restore_eligible_downloads f
JOIN backup.message_previews source ON source.id=f.source_id
JOIN restore_image_messages m ON m.id=source.message_id AND m.account_id=source.account_id AND m.conversation_id=source.conversation_id
LEFT JOIN message_previews local ON local.message_id=m.message_id AND local.url=source.url
LEFT JOIN resolved_links resolved ON resolved.entity_id=local.id
WHERE f.kind='image' AND f.has_bytes=1 AND source.download_state='ready' AND json_extract(source.payload,'$.Kind')='image'
  AND oie_quote_preview_allowed(m.payload,source.url)
  AND json_extract(source.payload,'$.FileHash')=f.hash
  AND (m.message_id=source.message_id OR (local.id IS NOT NULL AND json_extract(source.payload,'$.FileSize')=f.size))
  AND (local.id IS NULL OR (
      json_extract(local.payload,'$.Kind')='image'
      AND local.download_state='ready' AND json_extract(local.payload,'$.FileHash')=f.hash
  ) OR (
      local.download_state IN ('not_downloaded','failed') AND COALESCE(json_extract(local.payload,'$.FileHash'),'')=''
      AND (json_extract(local.payload,'$.Kind')='image' OR (
          json_extract(local.payload,'$.Kind')='link' AND local.state IN ('queued','unavailable') AND resolved.entity_id IS NULL
      ))
  ));


-- A verified original can be used without its derived thumbnail or favicon.
-- For other incomplete previews, let the engine derive a fresh cache entry.
INSERT INTO message_previews
SELECT p.id,p.revision_id,p.account_id,p.conversation_id,p.message_id,p.url,p.state,p.download_state,p.updated_at,
       CASE WHEN p.download_state='ready' THEN json_set(p.payload,
           '$.ImageHash',COALESCE(thumbnail.hash,''),'$.FaviconHash',COALESCE(favicon.hash,'')) ELSE p.payload END
FROM backup.message_previews p JOIN restore_messages x ON x.id=p.message_id
JOIN messages retained ON retained.id=x.id
LEFT JOIN message_content_heads head ON head.canonical_id=retained.id
LEFT JOIN restore_media_files thumbnail ON thumbnail.name='media/images/'||json_extract(p.payload,'$.ImageHash')
LEFT JOIN restore_media_files favicon ON favicon.name='media/images/'||json_extract(p.payload,'$.FaviconHash')
WHERE NOT EXISTS (SELECT 1 FROM restore_idless_direct_echoes e WHERE e.source_id=p.message_id)
AND oie_quote_preview_allowed(COALESCE(head.payload,retained.payload),p.url) AND (
    (p.download_state='ready' AND EXISTS (
        SELECT 1 FROM restore_downloads f WHERE f.kind='image' AND f.source_id=p.id
          AND f.hash=json_extract(p.payload,'$.FileHash')
    )) OR (
        p.download_state NOT IN ('ready','queued','downloading') AND COALESCE(json_extract(p.payload,'$.FileHash'),'')=''
        AND (thumbnail.hash IS NOT NULL OR COALESCE(json_extract(p.payload,'$.ImageHash'),'')='')
        AND (favicon.hash IS NOT NULL OR COALESCE(json_extract(p.payload,'$.FaviconHash'),'')='')
    )
)
ON CONFLICT DO NOTHING;
INSERT INTO preview_revisions
SELECT r.* FROM backup.preview_revisions r JOIN backup.message_previews b ON b.id=r.entity_id
JOIN message_previews p ON p.id=b.id AND p.message_id=b.message_id
JOIN restore_messages x ON x.id=b.message_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
