CREATE TEMP TABLE restore_download_devices (
    device_id TEXT PRIMARY KEY, days INTEGER NOT NULL, generation INTEGER NOT NULL,
    cleanup_revision TEXT NOT NULL DEFAULT '', adopt_cleanup INTEGER NOT NULL,
    has_membership_proofs INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

INSERT INTO restore_download_devices(device_id,days,generation,adopt_cleanup)
SELECT source.device_id,source.days,source.generation,COALESCE(destination.generation,0)=0
FROM backup.download_devices source LEFT JOIN download_devices destination ON destination.device_id=source.device_id;

CREATE TEMP TABLE restore_download_cleanup_parents (
    device_id TEXT NOT NULL, generation INTEGER NOT NULL, revision_id TEXT NOT NULL, parent_revision_id TEXT NOT NULL,
    PRIMARY KEY(device_id,generation)
) WITHOUT ROWID;

CREATE TEMP TABLE restore_download_membership_proofs (
    device_id TEXT NOT NULL, source_id TEXT NOT NULL, source_revision TEXT NOT NULL,
    kind TEXT NOT NULL, cleanup_revision_id TEXT NOT NULL,
    PRIMARY KEY(device_id,source_id,source_revision,kind)
) WITHOUT ROWID;
