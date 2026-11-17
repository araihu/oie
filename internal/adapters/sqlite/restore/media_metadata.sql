CREATE TEMP TABLE restore_media_files (name TEXT PRIMARY KEY, hash TEXT NOT NULL, size INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_media_files
SELECT json_extract(value,'$.name'),json_extract(value,'$.sha256'),json_extract(value,'$.size')
FROM json_each(CAST(@media_files AS TEXT));

INSERT INTO staged_attachments SELECT f.* FROM backup.staged_attachments f JOIN restore_chats x ON x.id=f.conversation_id
WHERE EXISTS(SELECT 1 FROM recovered_sends s JOIN json_each(s.payload,'$.Attachments') a
    WHERE s.conversation_id=f.conversation_id AND json_extract(a.value,'$.ID')=f.id)
OR EXISTS (SELECT 1 FROM messages m JOIN restore_messages x ON x.id=m.id
    JOIN json_each(m.payload,'$.Attachments') a
    WHERE m.conversation_id=f.conversation_id AND json_extract(a.value,'$.ID')=f.id)
ON CONFLICT(id) DO NOTHING;

-- A destination with cleanup history can acquire a membership only from
-- the same cleanup revision. Independent clear operations can have equal
-- numeric generations and older snapshots can lack a revision entirely.
CREATE TEMP TABLE restore_eligible_downloads AS
SELECT f.*
FROM backup.device_downloads f
JOIN restore_download_devices source ON source.device_id=f.device_id
LEFT JOIN download_devices destination ON destination.device_id=f.device_id
LEFT JOIN download_cleanup_revisions cleanup ON cleanup.device_id=f.device_id AND cleanup.generation=destination.generation
JOIN restore_media_files bytes ON bytes.hash=f.hash AND bytes.size=f.size
    AND bytes.name=(CASE f.kind WHEN 'image' THEN 'media/images/' ELSE 'media/downloads/' END)||f.hash
WHERE (COALESCE(destination.generation,0)=0 OR (
    destination.generation=source.generation AND cleanup.revision_id=source.cleanup_revision
))
  -- Settings claims legacy originals before clearing the real device.
  -- Older archives lack the identity of the device that claimed them.
  AND (f.device_id<>'legacy' OR NOT EXISTS (
      SELECT 1 FROM download_devices d WHERE d.device_id NOT IN ('','legacy') AND d.generation>0
  ))
  AND (COALESCE(destination.days,source.days)=0
       OR f.downloaded_at_ms>CAST(@now_ms AS INTEGER)-COALESCE(destination.days,source.days)*86400000);

CREATE TEMP VIEW restore_matching_media_messages AS
SELECT x.id, COALESCE(local_head.revision_id,local.revision_id) AS revision_id,
       COALESCE(local_head.payload,local.payload) AS payload
FROM restore_messages x
JOIN backup.messages source ON source.id=x.id
LEFT JOIN backup.message_content_heads source_head ON source_head.canonical_id=x.id
JOIN messages local ON local.id=x.id
LEFT JOIN message_content_heads local_head ON local_head.canonical_id=x.id
WHERE COALESCE(source_head.revision_id,source.revision_id)=COALESCE(local_head.revision_id,local.revision_id);

-- Reserve the matching file reference for a message still awaiting local
-- indexing. The catalog worker owns visibility and can reuse this ID.
INSERT INTO shared_content
SELECT DISTINCT source.*
FROM restore_eligible_downloads f
JOIN backup.shared_content source ON source.id=f.source_id
JOIN restore_matching_media_messages m ON m.id=source.message_id AND m.revision_id=f.source_revision
WHERE f.kind='file' AND oie_quote_preview_allowed(m.payload,source.url)
  AND NOT EXISTS (SELECT 1 FROM shared_content_state s WHERE s.message_id=source.message_id)
ON CONFLICT DO NOTHING;

CREATE TEMP TABLE restore_downloads AS
WITH resolved_links AS MATERIALIZED (
    SELECT DISTINCT entity_id FROM preview_revisions
    WHERE json_extract(payload,'$.Kind')='link' AND json_extract(payload,'$.State')='ready'
)
SELECT f.device_id, local.id AS source_id, f.source_revision, f.kind, f.hash, f.mime_type,
       f.size, f.verified_algorithm, f.downloaded_at_ms
FROM restore_eligible_downloads f
JOIN backup.shared_content source ON source.id=f.source_id
JOIN restore_matching_media_messages m ON m.id=source.message_id AND m.revision_id=f.source_revision
JOIN shared_content local ON local.message_id=source.message_id AND local.url=source.url AND local.kind=source.kind
WHERE f.kind='file' AND oie_quote_preview_allowed(m.payload,source.url)
UNION ALL
SELECT f.device_id, COALESCE(local.id,source.id), f.source_revision, f.kind, f.hash, f.mime_type,
       f.size, f.verified_algorithm, f.downloaded_at_ms
FROM restore_eligible_downloads f
JOIN backup.message_previews source ON source.id=f.source_id
JOIN restore_matching_media_messages m ON m.id=source.message_id
LEFT JOIN message_previews local ON local.message_id=source.message_id AND local.url=source.url
LEFT JOIN resolved_links resolved ON resolved.entity_id=local.id
WHERE f.kind='image' AND source.download_state='ready' AND json_extract(source.payload,'$.Kind')='image'
  AND oie_quote_preview_allowed(m.payload,source.url)
  AND json_extract(source.payload,'$.FileHash')=f.hash
  AND (local.id IS NULL OR (
      json_extract(local.payload,'$.Kind')='image'
      AND local.download_state='ready' AND json_extract(local.payload,'$.FileHash')=f.hash
  ) OR (
      local.download_state IN ('not_downloaded','failed') AND COALESCE(json_extract(local.payload,'$.FileHash'),'')=''
      AND (json_extract(local.payload,'$.Kind')='image' OR (
          json_extract(local.payload,'$.Kind')='link' AND local.state IN ('queued','unavailable') AND resolved.entity_id IS NULL
      ))
  ));
DROP VIEW restore_matching_media_messages;
DROP TABLE restore_eligible_downloads;

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
WHERE oie_quote_preview_allowed(COALESCE(head.payload,retained.payload),p.url) AND (
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
