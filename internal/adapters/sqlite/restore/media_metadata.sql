CREATE TEMP TABLE restore_media_files (name TEXT PRIMARY KEY, hash TEXT NOT NULL, size INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_media_files
SELECT json_extract(value,'$.name'),json_extract(value,'$.sha256'),json_extract(value,'$.size')
FROM json_each(CAST(@media_files AS TEXT));

INSERT INTO staged_attachments SELECT f.* FROM backup.staged_attachments f JOIN restore_chats x ON x.id=f.conversation_id
WHERE EXISTS(SELECT 1 FROM recovered_sends s JOIN json_each(s.payload,'$.Attachments') a
    WHERE s.conversation_id=f.conversation_id AND json_extract(a.value,'$.ID')=f.id)
OR EXISTS (SELECT 1 FROM messages m JOIN restore_messages x ON x.id=m.id
    JOIN json_each(m.payload,'$.Attachments') a
    WHERE m.conversation_id=f.conversation_id AND json_extract(a.value,'$.ID')=f.id)
ON CONFLICT(id) DO NOTHING;

-- A device's cleanup branch and a membership's own proof are independent.
-- A surviving download on a profile that merged a cleanup keeps its old proof.
CREATE TEMP TABLE restore_eligible_downloads AS
SELECT f.*, CASE
    WHEN proof.cleanup_revision_id IS NOT NULL THEN proof.cleanup_revision_id
    WHEN source.generation>0 AND source.has_membership_proofs=0 THEN ''
END AS cleanup_revision_id,@source_engine AS source_engine_id,
    bytes.name IS NOT NULL AS has_bytes, source.generation, source.cleanup_revision,
    (COALESCE(destination.generation,0)=0 AND source.generation>0
         AND (source.has_membership_proofs=0 OR proof.cleanup_revision_id=''))
    OR (f.device_id='legacy' AND f.kind='image' AND source.generation=0 AND known.cleared=0) AS defer_legacy
FROM backup.device_downloads f
JOIN restore_download_devices source ON source.device_id=f.device_id
CROSS JOIN (SELECT EXISTS(
    SELECT 1 FROM download_devices d WHERE d.device_id NOT IN ('','legacy') AND d.generation>0
) AS cleared) known
LEFT JOIN download_devices destination ON destination.device_id=f.device_id
LEFT JOIN download_cleanup_revisions cleanup ON cleanup.device_id=f.device_id AND cleanup.generation=destination.generation
LEFT JOIN restore_download_membership_proofs proof ON proof.device_id=f.device_id AND proof.source_id=f.source_id
    AND proof.source_revision=f.source_revision AND proof.kind=f.kind
LEFT JOIN restore_media_files bytes ON bytes.hash=f.hash AND bytes.size=f.size
    AND bytes.name=(CASE f.kind WHEN 'image' THEN 'media/images/' ELSE 'media/downloads/' END)||f.hash
LEFT JOIN legacy_download_restore_admissions admission ON admission.device_id=f.device_id
    AND admission.source_engine_id=@source_engine AND admission.source_id=f.source_id
    AND admission.source_revision=f.source_revision AND admission.kind=f.kind
    AND admission.hash=f.hash AND admission.size=f.size AND admission.downloaded_at_ms=f.downloaded_at_ms
    AND admission.generation=source.generation AND admission.generation=destination.generation
    AND admission.cleanup_revision=source.cleanup_revision AND admission.cleanup_revision=COALESCE(cleanup.revision_id,'')
LEFT JOIN unclaimed_legacy_download_restore_admissions unclaimed ON f.device_id='legacy' AND f.kind='image' AND source.generation=0
    AND unclaimed.source_engine_id=@source_engine AND unclaimed.source_id=f.source_id
    AND unclaimed.source_revision=f.source_revision AND unclaimed.hash=f.hash AND unclaimed.size=f.size
    AND unclaimed.downloaded_at_ms=f.downloaded_at_ms
WHERE (source.adopt_cleanup=1 OR (destination.generation=source.generation AND cleanup.revision_id=source.cleanup_revision)
       OR admission.device_id IS NOT NULL)
  AND (source.generation=0 OR (proof.cleanup_revision_id<>'' AND proof.cleanup_revision_id=source.cleanup_revision)
       OR ((COALESCE(destination.generation,0)=0 OR admission.device_id IS NOT NULL)
           AND (source.has_membership_proofs=0 OR proof.cleanup_revision_id='')))
  AND (has_bytes=1 OR defer_legacy=1)
  -- Settings claims legacy originals before clearing the real device.
  -- Older archives lack the identity of the device that claimed them.
  AND (f.device_id<>'legacy' OR known.cleared=0 OR unclaimed.source_engine_id IS NOT NULL)
  -- A Settings claim closes the original global membership even if the
  -- destination device expires its claimed bytes later.
  AND (f.device_id<>'legacy' OR f.kind<>'image' OR NOT EXISTS (
      SELECT 1 FROM backup.message_previews archived
      LEFT JOIN restore_idless_direct_echoes e ON e.source_id=archived.message_id
      JOIN legacy_image_claims claim ON claim.message_id IN (archived.message_id,COALESCE(e.retained_id,archived.message_id))
        AND claim.url=archived.url
        AND claim.source_revision=f.source_revision AND claim.hash=f.hash AND claim.size=f.size
      WHERE archived.id=f.source_id
  ))
  -- A separately observed named membership cannot revoke an exact global
  -- companion already admitted here before that evidence arrived.
  AND (f.device_id<>'legacy' OR f.kind<>'image' OR unclaimed.source_engine_id IS NOT NULL OR NOT EXISTS (
      SELECT 1 FROM backup.message_previews archived
      LEFT JOIN restore_idless_direct_echoes e ON e.source_id=archived.message_id
      WHERE archived.id=f.source_id AND (
          EXISTS (
              SELECT 1 FROM active_legacy_image_claims seen
              WHERE seen.message_id IN (archived.message_id,COALESCE(e.retained_id,archived.message_id)) AND seen.url=archived.url
                AND seen.source_revision=f.source_revision AND seen.hash=f.hash AND seen.size=f.size
          ) OR EXISTS (
              SELECT 1 FROM message_previews local
              JOIN device_downloads assigned ON assigned.source_id=local.id AND assigned.kind=f.kind
                  AND assigned.source_revision=f.source_revision AND assigned.hash=f.hash AND assigned.size=f.size
              WHERE local.message_id IN (archived.message_id,COALESCE(e.retained_id,archived.message_id))
                AND local.url=archived.url AND assigned.device_id NOT IN ('','legacy')
          )
      )
  ))
  AND (COALESCE(destination.days,source.days)=0
       OR f.downloaded_at_ms>CAST(@now_ms AS INTEGER)-COALESCE(destination.days,source.days)*86400000);

CREATE TEMP VIEW restore_matching_media_messages AS
SELECT x.id, COALESCE(local_head.revision_id,local.revision_id) AS revision_id,
       COALESCE(local_head.payload,local.payload) AS payload
FROM restore_messages x
JOIN backup.messages source ON source.id=x.id
LEFT JOIN backup.message_content_heads source_head ON source_head.canonical_id=x.id
JOIN messages local ON local.id=x.id
LEFT JOIN message_content_heads local_head ON local_head.canonical_id=x.id
WHERE COALESCE(source_head.revision_id,source.revision_id)=COALESCE(local_head.revision_id,local.revision_id);

-- The archive revision proves its membership. A separately observed
-- equivalent alias must bind it to the retained message and its revision.
CREATE TEMP VIEW restore_media_targets AS
SELECT m.id,m.revision_id AS source_revision,root.id AS message_id,root.account_id,root.conversation_id,
       COALESCE(head.revision_id,root.revision_id) AS revision_id,COALESCE(head.payload,root.payload) AS payload,
       (e.source_id IS NULL OR head.canonical_id IS NULL) AS accepts_files
FROM restore_matching_media_messages m
LEFT JOIN restore_idless_direct_echoes e ON e.source_id=m.id
JOIN messages root ON root.id=COALESCE(e.retained_id,m.id)
LEFT JOIN message_content_heads head ON head.canonical_id=root.id;

-- Remember legacy media that an untouched device could import already.
-- Only a later snapshot with the same source and membership can fill it.
CREATE TEMP TABLE restore_pending_legacy_downloads AS
SELECT f.device_id,f.source_engine_id,f.source_id,f.source_revision,
       f.kind,f.hash,f.size,f.downloaded_at_ms,f.generation,f.cleanup_revision
FROM restore_eligible_downloads f
WHERE f.defer_legacy=1 AND f.has_bytes=0 AND length(f.hash)=64 AND f.hash NOT GLOB '*[^0-9a-f]*' AND f.size>=0
  AND (
      f.kind='file' AND EXISTS (
          SELECT 1 FROM backup.shared_content ref
          JOIN restore_media_targets m ON m.id=ref.message_id AND m.source_revision=f.source_revision AND m.accepts_files=1
          WHERE ref.id=f.source_id AND oie_quote_preview_allowed(m.payload,ref.url)
      ) OR f.kind='image' AND EXISTS (
          SELECT 1 FROM backup.message_previews p JOIN restore_matching_media_messages m ON m.id=p.message_id
          WHERE p.id=f.source_id AND p.download_state='ready' AND json_extract(p.payload,'$.Kind')='image'
            AND json_extract(p.payload,'$.FileHash')=f.hash AND oie_quote_preview_allowed(m.payload,p.url)
      )
  );

-- Reserve the matching file reference for a message still awaiting local
-- indexing. The catalog worker owns visibility and can reuse this ID.
INSERT INTO shared_content
SELECT DISTINCT source.*
FROM restore_eligible_downloads f
JOIN backup.shared_content source ON source.id=f.source_id
JOIN restore_media_targets m ON m.id=source.message_id AND m.message_id=source.message_id AND m.source_revision=f.source_revision
WHERE f.kind='file' AND f.has_bytes=1 AND oie_quote_preview_allowed(m.payload,source.url)
  AND NOT EXISTS (SELECT 1 FROM shared_content_state s WHERE s.message_id=source.message_id)
ON CONFLICT DO NOTHING;
