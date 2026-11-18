UPDATE restore_download_devices SET has_membership_proofs=1;

INSERT INTO restore_download_membership_proofs SELECT * FROM backup.download_membership_cleanup_revisions;

-- Follow recorded parents from each current source revision. A legacy or
-- unrelated revision cannot prove ancestry by its numeric generation alone.
INSERT INTO restore_download_cleanup_parents
WITH RECURSIVE lineage(device_id,generation,revision_id,parent_revision_id) AS (
    SELECT d.device_id,d.generation,d.cleanup_revision,COALESCE(p.parent_revision_id,'')
    FROM restore_download_devices d
    LEFT JOIN backup.download_cleanup_parents p
      ON p.device_id=d.device_id AND p.generation=d.generation AND p.revision_id=d.cleanup_revision
    WHERE d.cleanup_revision<>''
    UNION ALL
    SELECT l.device_id,l.generation-1,l.parent_revision_id,COALESCE(p.parent_revision_id,'')
    FROM lineage l LEFT JOIN backup.download_cleanup_parents p
      ON p.device_id=l.device_id AND p.generation=l.generation-1 AND p.revision_id=l.parent_revision_id
    WHERE l.generation>1 AND l.parent_revision_id<>''
)
SELECT * FROM lineage;

UPDATE restore_download_devices AS source SET adopt_cleanup=1
WHERE EXISTS (
    SELECT 1 FROM download_devices d JOIN download_cleanup_revisions r
      ON r.device_id=d.device_id AND r.generation=d.generation
    JOIN restore_download_cleanup_parents p
      ON p.device_id=r.device_id AND p.generation=r.generation AND p.revision_id=r.revision_id
    WHERE d.device_id=source.device_id AND d.generation<source.generation
);
