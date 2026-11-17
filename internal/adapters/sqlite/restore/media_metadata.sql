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

-- A destination device's generation advances on deletion. Independent
-- profiles can reach the same generation after different deletions, so
-- only an untouched or new device can acquire old download memberships.
CREATE TEMP TABLE restore_downloads AS
WITH eligible AS (
    SELECT f.*
    FROM backup.device_downloads f
    JOIN backup.download_devices source ON source.device_id=f.device_id
    LEFT JOIN download_devices destination ON destination.device_id=f.device_id
    JOIN restore_media_files bytes ON bytes.hash=f.hash AND bytes.size=f.size
        AND bytes.name=(CASE f.kind WHEN 'image' THEN 'media/images/' ELSE 'media/downloads/' END)||f.hash
    WHERE (destination.device_id IS NULL OR (destination.generation=0 AND source.generation=0))
      AND (COALESCE(destination.days,source.days)=0
           OR f.downloaded_at_ms>CAST(@now_ms AS INTEGER)-COALESCE(destination.days,source.days)*86400000)
), matching_messages AS (
    SELECT x.id, COALESCE(local_head.revision_id,local.revision_id) AS revision_id
    FROM restore_messages x
    JOIN backup.messages source ON source.id=x.id
    LEFT JOIN backup.message_content_heads source_head ON source_head.canonical_id=x.id
    JOIN messages local ON local.id=x.id
    LEFT JOIN message_content_heads local_head ON local_head.canonical_id=x.id
    WHERE COALESCE(source_head.revision_id,source.revision_id)=COALESCE(local_head.revision_id,local.revision_id)
), resolved_links AS MATERIALIZED (
    SELECT DISTINCT entity_id FROM preview_revisions
    WHERE json_extract(payload,'$.Kind')='link' AND json_extract(payload,'$.State')='ready'
)
SELECT f.device_id, local.id AS source_id, f.source_revision, f.kind, f.hash, f.mime_type,
       f.size, f.verified_algorithm, f.downloaded_at_ms
FROM eligible f
JOIN backup.shared_content source ON source.id=f.source_id
JOIN matching_messages m ON m.id=source.message_id AND m.revision_id=f.source_revision
JOIN shared_content local ON local.message_id=source.message_id AND local.url=source.url AND local.kind=source.kind
WHERE f.kind='file'
UNION ALL
SELECT f.device_id, COALESCE(local.id,source.id), f.source_revision, f.kind, f.hash, f.mime_type,
       f.size, f.verified_algorithm, f.downloaded_at_ms
FROM eligible f
JOIN backup.message_previews source ON source.id=f.source_id
JOIN matching_messages m ON m.id=source.message_id
LEFT JOIN message_previews local ON local.message_id=source.message_id AND local.url=source.url
LEFT JOIN resolved_links resolved ON resolved.entity_id=local.id
WHERE f.kind='image' AND source.download_state='ready' AND json_extract(source.payload,'$.Kind')='image'
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

-- Preview rows are a cache. If referenced files are missing, let the engine
-- derive a fresh preview from the restored message, with a usable download
-- action. Queued or interrupted downloads belong to the original profile.
INSERT INTO message_previews
SELECT p.* FROM backup.message_previews p JOIN restore_messages x ON x.id=p.message_id
WHERE p.download_state NOT IN ('queued','downloading')
AND (
    COALESCE(json_extract(p.payload,'$.ImageHash'),'')=''
    OR EXISTS (SELECT 1 FROM restore_media_files f WHERE f.name='media/images/'||json_extract(p.payload,'$.ImageHash'))
)
AND (
    COALESCE(json_extract(p.payload,'$.FaviconHash'),'')=''
    OR EXISTS (SELECT 1 FROM restore_media_files f WHERE f.name='media/images/'||json_extract(p.payload,'$.FaviconHash'))
)
AND (
    p.download_state<>'ready' AND COALESCE(json_extract(p.payload,'$.FileHash'),'')=''
    OR EXISTS (SELECT 1 FROM restore_downloads f WHERE f.kind='image' AND f.source_id=p.id)
)
ON CONFLICT DO NOTHING;
INSERT INTO preview_revisions
SELECT r.* FROM backup.preview_revisions r JOIN backup.message_previews b ON b.id=r.entity_id
JOIN message_previews p ON p.id=b.id AND p.message_id=b.message_id
JOIN restore_messages x ON x.id=b.message_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
