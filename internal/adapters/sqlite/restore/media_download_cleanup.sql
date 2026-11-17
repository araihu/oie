UPDATE restore_download_devices AS d SET cleanup_revision=r.revision_id
FROM backup.download_cleanup_revisions r WHERE d.device_id=r.device_id AND d.generation=r.generation;
