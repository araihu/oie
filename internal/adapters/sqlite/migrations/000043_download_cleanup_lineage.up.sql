CREATE TABLE download_cleanup_revisions (
    device_id TEXT PRIMARY KEY REFERENCES download_devices(device_id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK(generation > 0),
    revision_id TEXT NOT NULL CHECK(revision_id <> '')
) WITHOUT ROWID;
