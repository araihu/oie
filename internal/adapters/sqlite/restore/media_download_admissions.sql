-- An untouched destination can adopt a prior partial restore under the exact
-- archived cleanup marker. The original backup still has to supply its bytes.
INSERT INTO restore_pending_legacy_downloads
SELECT a.* FROM backup.legacy_download_restore_admissions a
JOIN restore_download_devices source ON source.device_id=a.device_id AND source.generation=a.generation
    AND source.cleanup_revision=a.cleanup_revision
LEFT JOIN download_devices destination ON destination.device_id=a.device_id
WHERE COALESCE(destination.generation,0)=0;
