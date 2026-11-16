CREATE TABLE download_devices (
 device_id TEXT PRIMARY KEY,
 days INTEGER NOT NULL DEFAULT 0 CHECK(days BETWEEN 0 AND 3650),
 generation INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE device_downloads (
 device_id TEXT NOT NULL REFERENCES download_devices(device_id),
 source_id TEXT NOT NULL,
 source_revision TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('file','image')),
 hash TEXT NOT NULL,
 mime_type TEXT NOT NULL,
 size INTEGER NOT NULL,
 verified_algorithm TEXT NOT NULL,
 downloaded_at_ms INTEGER NOT NULL,
 PRIMARY KEY(device_id, source_id, source_revision, kind)
);
CREATE INDEX device_downloads_hash ON device_downloads(kind,hash);
CREATE INDEX device_downloads_source ON device_downloads(kind,source_id);
CREATE INDEX device_downloads_expiry ON device_downloads(device_id,downloaded_at_ms);
CREATE TABLE download_garbage (
 kind TEXT NOT NULL,
 hash TEXT NOT NULL,
 PRIMARY KEY(kind,hash)
);
-- Old originals have no browser provenance. Preserve those references until
-- the local installation first configures its storage policy.
INSERT INTO download_devices(device_id)
SELECT 'legacy' WHERE EXISTS(SELECT 1 FROM message_previews WHERE download_state='ready');
INSERT INTO device_downloads(device_id,source_id,source_revision,kind,hash,mime_type,size,verified_algorithm,downloaded_at_ms)
SELECT 'legacy',id,'','image',json_extract(payload,'$.FileHash'),json_extract(payload,'$.FileMimeType'),
       json_extract(payload,'$.FileSize'),COALESCE(json_extract(payload,'$.VerifiedAlgorithm'),''),
       CAST(COALESCE(unixepoch(updated_at),unixepoch()) AS INTEGER)*1000
FROM message_previews
WHERE download_state='ready' AND COALESCE(json_extract(payload,'$.FileHash'),'')<>''
 AND COALESCE(json_extract(payload,'$.FileSize'),0)>0;
PRAGMA user_version=34;
