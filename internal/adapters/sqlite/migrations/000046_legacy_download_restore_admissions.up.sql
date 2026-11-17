CREATE TABLE legacy_download_restore_admissions (
    device_id TEXT NOT NULL REFERENCES download_devices(device_id) ON DELETE CASCADE,
    source_engine_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('image','file')),
    hash TEXT NOT NULL,
    size INTEGER NOT NULL CHECK(size >= 0),
    downloaded_at_ms INTEGER NOT NULL,
    generation INTEGER NOT NULL CHECK(generation > 0),
    cleanup_revision TEXT NOT NULL,
    PRIMARY KEY(device_id,source_engine_id,source_id,source_revision,kind,hash,size,downloaded_at_ms)
) WITHOUT ROWID;

CREATE TRIGGER clear_legacy_download_restore_admissions
AFTER UPDATE OF generation ON download_devices WHEN NEW.generation<>OLD.generation
BEGIN
    DELETE FROM legacy_download_restore_admissions WHERE device_id=NEW.device_id;
END;
