-- name: GetDownloadDevice :one
SELECT days,generation FROM download_devices WHERE device_id=?;

-- name: EnsureDownloadDevice :exec
INSERT INTO download_devices(device_id) VALUES(?) ON CONFLICT DO NOTHING;

-- name: SetDownloadDeviceDays :exec
INSERT INTO download_devices(device_id,days) VALUES(?,?)
ON CONFLICT(device_id) DO UPDATE SET days=excluded.days;

-- name: AdvanceDownloadGeneration :exec
INSERT INTO download_devices(device_id,generation) VALUES(?,1)
ON CONFLICT(device_id) DO UPDATE SET generation=generation+1;

-- name: PutDownloadCleanupRevision :exec
INSERT INTO download_cleanup_revisions(device_id,generation,revision_id)
SELECT d.device_id,d.generation,sqlc.arg(revision_id) FROM download_devices d WHERE d.device_id=sqlc.arg(device_id)
ON CONFLICT(device_id) DO UPDATE SET generation=excluded.generation,revision_id=excluded.revision_id;

-- name: PutDownloadCleanupParent :exec
INSERT INTO download_cleanup_parents(device_id,generation,revision_id,parent_revision_id)
SELECT d.device_id,d.generation,sqlc.arg(revision_id),r.revision_id
FROM download_devices d JOIN download_cleanup_revisions r ON r.device_id=d.device_id
WHERE d.device_id=sqlc.arg(device_id) AND d.generation=r.generation+1;

-- name: GetDeviceDownload :one
SELECT * FROM device_downloads WHERE device_id=? AND source_id=? AND source_revision=? AND kind='file';

-- name: InsertDeviceDownload :execrows
INSERT INTO device_downloads(device_id,source_id,source_revision,kind,hash,mime_type,size,verified_algorithm,downloaded_at_ms)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(device_id,source_id,source_revision,kind) DO UPDATE
SET hash=excluded.hash,mime_type=excluded.mime_type,size=excluded.size,
    verified_algorithm=excluded.verified_algorithm,downloaded_at_ms=excluded.downloaded_at_ms
WHERE excluded.kind='file';

-- name: PutDownloadMembershipCleanupRevision :exec
INSERT INTO download_membership_cleanup_revisions(device_id,source_id,source_revision,kind,cleanup_revision_id)
SELECT d.device_id,sqlc.arg(source_id),sqlc.arg(source_revision),sqlc.arg(kind),r.revision_id
FROM download_devices d JOIN download_cleanup_revisions r ON r.device_id=d.device_id AND r.generation=d.generation
WHERE d.device_id=sqlc.arg(device_id)
ON CONFLICT(device_id,source_id,source_revision,kind) DO UPDATE SET cleanup_revision_id=excluded.cleanup_revision_id;

-- name: KeepLegacyDownloadMembership :exec
INSERT INTO legacy_download_memberships(device_id,source_id,source_revision,kind)
SELECT d.device_id,sqlc.arg(source_id),sqlc.arg(source_revision),sqlc.arg(kind) FROM download_devices d
WHERE d.device_id=sqlc.arg(device_id) AND d.generation>0 AND NOT EXISTS (
  SELECT 1 FROM download_cleanup_revisions r WHERE r.device_id=d.device_id AND r.generation=d.generation
)
ON CONFLICT DO NOTHING;

-- name: KeepLegacyImageCompletions :exec
INSERT INTO legacy_download_memberships(device_id,source_id,source_revision,kind)
SELECT f.device_id,f.source_id,f.source_revision,f.kind
FROM device_downloads f JOIN download_devices d ON d.device_id=f.device_id
WHERE f.kind='image' AND f.source_id=sqlc.arg(source_id) AND f.hash='' AND d.generation>0 AND NOT EXISTS (
  SELECT 1 FROM download_cleanup_revisions r WHERE r.device_id=d.device_id AND r.generation=d.generation
)
ON CONFLICT DO NOTHING;

-- name: FinishImageDownloads :exec
UPDATE device_downloads SET hash=?,mime_type=?,size=?,verified_algorithm=?,downloaded_at_ms=?
WHERE kind='image' AND source_id=? AND hash='';

-- name: DiscardPendingImageDownloads :exec
DELETE FROM device_downloads WHERE kind='image' AND source_id=? AND hash='';

-- name: CountDeviceDownloads :one
SELECT count(*) FROM device_downloads WHERE device_id=? AND hash<>'';

-- name: DropDeviceDownloads :many
DELETE FROM device_downloads WHERE device_id=? RETURNING *;

-- name: DropExpiredDownloads :many
DELETE FROM device_downloads
WHERE hash<>'' AND EXISTS(
 SELECT 1 FROM download_devices d WHERE d.device_id=device_downloads.device_id AND d.days>0
 AND device_downloads.downloaded_at_ms <= CAST(sqlc.arg(now_ms) AS INTEGER)-d.days*86400000
)
RETURNING *;

-- name: CountDownloadsByHash :one
SELECT count(*) FROM device_downloads WHERE kind=? AND hash=?;

-- name: CountImageDownloadSources :one
SELECT count(*) FROM device_downloads WHERE kind='image' AND source_id=?;

-- name: QueueDownloadGarbage :exec
INSERT INTO download_garbage(kind,hash) VALUES(?,?) ON CONFLICT DO NOTHING;

-- name: ListDownloadGarbage :many
SELECT * FROM download_garbage;

-- name: RemoveDownloadGarbage :exec
DELETE FROM download_garbage WHERE kind=? AND hash=?;

-- name: ImagePreviewStillUsesHash :one
SELECT EXISTS(SELECT 1 FROM message_previews WHERE
 json_extract(payload,'$.FileHash')=sqlc.arg(hash) OR
 json_extract(payload,'$.ImageHash')=sqlc.arg(hash) OR
 json_extract(payload,'$.FaviconHash')=sqlc.arg(hash));

-- name: DeleteDownloadImageFile :exec
DELETE FROM media_files WHERE hash=?;

-- name: CopyLegacyImageDownloads :exec
INSERT INTO device_downloads(device_id,source_id,source_revision,kind,hash,mime_type,size,verified_algorithm,downloaded_at_ms)
SELECT sqlc.arg(device_id),source_id,source_revision,kind,hash,mime_type,size,verified_algorithm,downloaded_at_ms
FROM device_downloads WHERE device_id='legacy'
ON CONFLICT DO NOTHING;

-- name: RemoveLegacyDownloadDevice :exec
DELETE FROM download_devices WHERE device_id='legacy';

-- name: ListRetainedDownloadHashes :many
SELECT DISTINCT hash FROM device_downloads WHERE kind='file';
