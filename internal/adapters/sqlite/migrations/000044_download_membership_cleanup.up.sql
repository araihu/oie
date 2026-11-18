CREATE TABLE download_cleanup_parents (
    device_id TEXT NOT NULL REFERENCES download_devices(device_id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK(generation > 1),
    revision_id TEXT NOT NULL CHECK(revision_id <> ''),
    parent_revision_id TEXT NOT NULL CHECK(parent_revision_id <> '' AND parent_revision_id <> revision_id),
    PRIMARY KEY(device_id, revision_id)
) WITHOUT ROWID;

CREATE TABLE download_membership_cleanup_revisions (
    device_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    kind TEXT NOT NULL,
    cleanup_revision_id TEXT NOT NULL CHECK(cleanup_revision_id <> ''),
    PRIMARY KEY(device_id, source_id, source_revision, kind),
    FOREIGN KEY(device_id, source_id, source_revision, kind)
        REFERENCES device_downloads(device_id, source_id, source_revision, kind) ON DELETE CASCADE
) WITHOUT ROWID;
