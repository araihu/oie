CREATE TABLE legacy_download_memberships (
    device_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    kind TEXT NOT NULL,
    PRIMARY KEY(device_id, source_id, source_revision, kind),
    FOREIGN KEY(device_id, source_id, source_revision, kind)
        REFERENCES device_downloads(device_id, source_id, source_revision, kind) ON DELETE CASCADE
) WITHOUT ROWID;

-- Numbered generations from before cleanup revisions had no shared lineage.
-- Give each local baseline its own identity before certifying its memberships.
CREATE TEMP TABLE migration_download_baselines AS
WITH clock(ms) AS (SELECT printf('%012x', CAST(unixepoch('subsec') * 1000 AS INTEGER)))
SELECT d.device_id, d.generation,
  substr(ms,1,8)||'-'||substr(ms,9,4)||'-7'||lower(substr(hex(randomblob(2)),1,3))||'-'||
  substr('89ab',1+(random()&3),1)||lower(substr(hex(randomblob(2)),1,3))||'-'||lower(hex(randomblob(6))) AS revision_id
FROM download_devices d CROSS JOIN clock
WHERE d.generation>0 AND NOT EXISTS (SELECT 1 FROM download_cleanup_revisions r WHERE r.device_id=d.device_id);

INSERT INTO download_cleanup_revisions(device_id,generation,revision_id)
SELECT device_id,generation,revision_id FROM migration_download_baselines;

INSERT INTO download_membership_cleanup_revisions(device_id,source_id,source_revision,kind,cleanup_revision_id)
SELECT f.device_id,f.source_id,f.source_revision,f.kind,b.revision_id
FROM device_downloads f JOIN migration_download_baselines b ON b.device_id=f.device_id
ON CONFLICT DO NOTHING;

DROP TABLE migration_download_baselines;

-- A recorded cleanup may have come from another profile. Existing downloads
-- without their own proof remain portable to an untouched device only.
INSERT INTO legacy_download_memberships(device_id,source_id,source_revision,kind)
SELECT f.device_id,f.source_id,f.source_revision,f.kind
FROM device_downloads f JOIN download_devices d ON d.device_id=f.device_id
WHERE d.generation>0 AND NOT EXISTS (
    SELECT 1 FROM download_membership_cleanup_revisions p WHERE p.device_id=f.device_id
      AND p.source_id=f.source_id AND p.source_revision=f.source_revision AND p.kind=f.kind
);
