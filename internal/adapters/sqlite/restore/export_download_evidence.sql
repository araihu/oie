-- A download cleanup revision generated while migrating a private snapshot
-- does not exist on the exporting device. Keep the memberships exportable
-- without claiming they descend from that transient revision.
INSERT OR IGNORE INTO legacy_download_memberships(device_id,source_id,source_revision,kind)
SELECT m.device_id,m.source_id,m.source_revision,m.kind
FROM download_membership_cleanup_revisions m JOIN download_cleanup_revisions r
  ON r.device_id=m.device_id AND r.revision_id=m.cleanup_revision_id
WHERE NOT EXISTS(SELECT 1 FROM export_existing_cleanups e WHERE e.revision_id=r.revision_id AND e.device_id=r.device_id);
DELETE FROM download_membership_cleanup_revisions AS m
WHERE EXISTS(SELECT 1 FROM download_cleanup_revisions r WHERE r.device_id=m.device_id AND r.revision_id=m.cleanup_revision_id)
 AND NOT EXISTS(SELECT 1 FROM export_existing_cleanups e WHERE e.revision_id=m.cleanup_revision_id AND e.device_id=m.device_id);
DELETE FROM download_cleanup_revisions AS r
WHERE NOT EXISTS(SELECT 1 FROM export_existing_cleanups e WHERE e.revision_id=r.revision_id AND e.device_id=r.device_id);
DROP TABLE export_existing_cleanups;
