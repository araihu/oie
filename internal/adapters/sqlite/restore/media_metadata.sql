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
SELECT f.*
FROM backup.device_downloads f
JOIN backup.download_devices source ON source.device_id=f.device_id
LEFT JOIN download_devices destination ON destination.device_id=f.device_id
JOIN restore_media_files bytes ON bytes.hash=f.hash AND bytes.size=f.size
    AND bytes.name=(CASE f.kind WHEN 'image' THEN 'media/images/' ELSE 'media/downloads/' END)||f.hash
WHERE (destination.device_id IS NULL OR (destination.generation=0 AND source.generation=0))
AND (COALESCE(destination.days,source.days)=0
     OR f.downloaded_at_ms>CAST(@now_ms AS INTEGER)-COALESCE(destination.days,source.days)*86400000)
AND (
    f.kind='file' AND EXISTS(
        SELECT 1 FROM shared_content ref JOIN restore_messages x ON x.id=ref.message_id WHERE ref.id=f.source_id
    )
    OR f.kind='image' AND EXISTS(
        SELECT 1 FROM backup.message_previews p JOIN restore_messages x ON x.id=p.message_id
        WHERE p.id=f.source_id AND json_extract(p.payload,'$.FileHash')=f.hash
    )
);

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

INSERT INTO media_files SELECT f.* FROM backup.media_files f
JOIN restore_media_files bytes ON bytes.name='media/images/'||f.hash AND bytes.size=f.size
WHERE EXISTS (SELECT 1 FROM message_previews p JOIN restore_messages x ON x.id=p.message_id
    WHERE f.hash IN (json_extract(p.payload,'$.FileHash'), json_extract(p.payload,'$.ImageHash'), json_extract(p.payload,'$.FaviconHash')))
ON CONFLICT(hash) DO NOTHING;

DELETE FROM restore_downloads
WHERE kind='image' AND NOT EXISTS(
    SELECT 1 FROM message_previews p WHERE p.id=restore_downloads.source_id
    AND json_extract(p.payload,'$.FileHash')=restore_downloads.hash
);
INSERT INTO download_devices
SELECT DISTINCT d.device_id,d.days,d.generation FROM backup.download_devices d
JOIN restore_downloads f ON f.device_id=d.device_id
WHERE TRUE ON CONFLICT(device_id) DO NOTHING;

INSERT INTO device_downloads
SELECT f.* FROM restore_downloads f WHERE TRUE ON CONFLICT DO NOTHING;

DROP TABLE restore_downloads;
DROP TABLE restore_media_files;
