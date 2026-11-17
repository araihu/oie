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
-- The device keeps its cleanup generation and revision together. A cleanup
-- from another branch can initialize an untouched device only.
INSERT INTO download_devices
SELECT d.device_id,d.days,d.generation FROM restore_download_devices d
WHERE d.generation>0 OR EXISTS (SELECT 1 FROM restore_downloads f WHERE f.device_id=d.device_id)
ON CONFLICT(device_id) DO UPDATE SET generation=excluded.generation
WHERE download_devices.generation=0 AND excluded.generation>0;

INSERT INTO download_cleanup_revisions(device_id,generation,revision_id)
SELECT device_id,generation,cleanup_revision FROM restore_download_devices
WHERE adopt_cleanup=1 AND cleanup_revision<>''
ON CONFLICT(device_id) DO NOTHING;

INSERT INTO device_downloads
SELECT f.* FROM restore_downloads f WHERE TRUE ON CONFLICT DO NOTHING;

DROP TABLE restore_downloads;
DROP TABLE restore_download_devices;
DROP TABLE restore_media_files;
