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
-- Advance an untouched device or one with a proven ancestor of the new head.
INSERT INTO download_devices
SELECT d.device_id,d.days,d.generation FROM restore_download_devices d
WHERE d.generation>0 OR EXISTS (SELECT 1 FROM restore_downloads f WHERE f.device_id=d.device_id)
ON CONFLICT(device_id) DO UPDATE SET generation=excluded.generation
WHERE excluded.generation>download_devices.generation AND EXISTS(
    SELECT 1 FROM restore_download_devices d WHERE d.device_id=excluded.device_id AND d.adopt_cleanup=1
);

INSERT INTO download_cleanup_revisions(device_id,generation,revision_id)
SELECT device_id,generation,cleanup_revision FROM restore_download_devices
WHERE adopt_cleanup=1 AND cleanup_revision<>''
ON CONFLICT(device_id) DO UPDATE SET generation=excluded.generation,revision_id=excluded.revision_id
WHERE excluded.generation>download_cleanup_revisions.generation;

INSERT INTO download_cleanup_parents
SELECT p.* FROM restore_download_cleanup_parents p JOIN restore_download_devices d ON d.device_id=p.device_id
JOIN download_cleanup_revisions r ON r.device_id=d.device_id AND r.generation=d.generation AND r.revision_id=d.cleanup_revision
WHERE p.parent_revision_id<>'' ON CONFLICT DO NOTHING;

-- A conflicting local membership keeps both its bytes and its original proof.
DELETE FROM restore_downloads AS f WHERE EXISTS (
    SELECT 1 FROM device_downloads d WHERE d.device_id=f.device_id AND d.source_id=f.source_id
      AND d.source_revision=f.source_revision AND d.kind=f.kind
);

INSERT INTO device_downloads(device_id,source_id,source_revision,kind,hash,mime_type,size,verified_algorithm,downloaded_at_ms)
SELECT device_id,source_id,source_revision,kind,hash,mime_type,size,verified_algorithm,downloaded_at_ms
FROM restore_downloads;

INSERT INTO download_membership_cleanup_revisions(device_id,source_id,source_revision,kind,cleanup_revision_id)
SELECT device_id,source_id,source_revision,kind,cleanup_revision_id FROM restore_downloads WHERE cleanup_revision_id<>'';

INSERT INTO legacy_download_memberships(device_id,source_id,source_revision,kind)
SELECT device_id,source_id,source_revision,kind FROM restore_downloads WHERE cleanup_revision_id='';

DROP TABLE restore_downloads;
DROP TABLE restore_download_devices;
DROP TABLE restore_download_cleanup_parents;
DROP TABLE restore_download_membership_proofs;
DROP TABLE restore_media_files;
