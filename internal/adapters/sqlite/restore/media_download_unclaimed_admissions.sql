-- Carry the original source scope only onto a destination with no device
-- cleanup history. A later clear removes these pending admissions.
INSERT INTO restore_pending_legacy_downloads
SELECT 'legacy',a.source_engine_id,a.source_id,a.source_revision,'image',a.hash,a.size,a.downloaded_at_ms,0,''
FROM backup.unclaimed_legacy_download_restore_admissions a
WHERE NOT EXISTS (SELECT 1 FROM download_devices d WHERE d.device_id NOT IN ('','legacy') AND d.generation>0);
