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
