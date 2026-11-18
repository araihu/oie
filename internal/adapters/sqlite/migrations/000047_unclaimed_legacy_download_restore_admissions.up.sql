CREATE TABLE unclaimed_legacy_download_restore_admissions (
    source_engine_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    hash TEXT NOT NULL,
    size INTEGER NOT NULL CHECK(size >= 0),
    downloaded_at_ms INTEGER NOT NULL,
    PRIMARY KEY(source_engine_id,source_id,source_revision,hash,size,downloaded_at_ms)
) WITHOUT ROWID;

CREATE TRIGGER clear_unclaimed_legacy_download_admissions_on_insert
AFTER INSERT ON download_devices WHEN NEW.device_id NOT IN ('','legacy') AND NEW.generation>0
BEGIN
    DELETE FROM unclaimed_legacy_download_restore_admissions;
END;

CREATE TRIGGER clear_unclaimed_legacy_download_admissions_on_update
AFTER UPDATE OF generation ON download_devices
WHEN NEW.device_id NOT IN ('','legacy') AND NEW.generation<>OLD.generation
BEGIN
    DELETE FROM unclaimed_legacy_download_restore_admissions;
END;
