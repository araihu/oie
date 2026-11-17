CREATE TEMP TABLE restore_download_devices (
    device_id TEXT PRIMARY KEY, days INTEGER NOT NULL, generation INTEGER NOT NULL,
    cleanup_revision TEXT NOT NULL DEFAULT '', adopt_cleanup INTEGER NOT NULL
) WITHOUT ROWID;

INSERT INTO restore_download_devices(device_id,days,generation,adopt_cleanup)
SELECT source.device_id,source.days,source.generation,COALESCE(destination.generation,0)=0
FROM backup.download_devices source LEFT JOIN download_devices destination ON destination.device_id=source.device_id;
