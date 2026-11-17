-- A stopped older profile may have written an archive without first running
-- the in-place backfill. Read the same exact detach evidence from its history.
WITH RECURSIVE image_history(preview_id,revision_id) AS (
    SELECT p.id,p.revision_id
    FROM backup.message_previews p JOIN restore_messages selected ON selected.id=p.message_id
    JOIN messages retained ON retained.id=p.message_id AND retained.account_id=p.account_id AND retained.conversation_id=p.conversation_id
    LEFT JOIN message_content_heads head ON head.canonical_id=retained.id
    WHERE json_extract(p.payload,'$.Kind')='image'
      AND oie_quote_preview_allowed(COALESCE(head.payload,retained.payload),p.url)
    UNION
    SELECT history.preview_id,parent.id
    FROM image_history history
    JOIN backup.preview_revisions child ON child.id=history.revision_id AND child.entity_id=history.preview_id
    JOIN json_each(child.parents) edge ON edge.type='text'
    JOIN backup.preview_revisions parent ON parent.id=edge.value AND parent.entity_id=child.entity_id
), retired AS (
    SELECT p.id AS preview_id,p.message_id,p.url,json_extract(ready.payload,'$.FileHash') AS hash,
           json_extract(ready.payload,'$.FileSize') AS size
    FROM image_history history JOIN backup.message_previews p ON p.id=history.preview_id
    JOIN backup.preview_revisions detached ON detached.id=history.revision_id AND detached.entity_id=p.id
    JOIN backup.preview_revisions ready ON ready.id=json_extract(detached.parents,'$[0]') AND ready.entity_id=p.id
    WHERE json_array_length(detached.parents)=1
      AND json_extract(detached.payload,'$.ID')=p.id AND json_extract(ready.payload,'$.ID')=p.id
      AND json_extract(detached.payload,'$.MessageID')=p.message_id AND json_extract(ready.payload,'$.MessageID')=p.message_id
      AND json_extract(detached.payload,'$.URL')=p.url AND json_extract(ready.payload,'$.URL')=p.url
      AND json_extract(detached.payload,'$.Kind')='image' AND json_extract(ready.payload,'$.Kind')='image'
      AND json_extract(detached.payload,'$.DownloadState')='not_downloaded'
      AND json_extract(detached.payload,'$.FileHash')='' AND json_extract(detached.payload,'$.FileMimeType')=''
      AND json_extract(detached.payload,'$.FileSize')=0 AND json_extract(detached.payload,'$.LastErrorCode')=''
      AND json_extract(ready.payload,'$.DownloadState')='ready'
      AND length(json_extract(ready.payload,'$.FileHash'))=64 AND json_extract(ready.payload,'$.FileHash') NOT GLOB '*[^0-9a-f]*'
      AND json_type(ready.payload,'$.FileSize')='integer' AND json_extract(ready.payload,'$.FileSize')>0
)
INSERT INTO legacy_image_claims(message_id,url,source_revision,hash,size)
SELECT r.message_id,r.url,'',r.hash,r.size FROM retired r
WHERE NOT EXISTS (
    SELECT 1 FROM backup.device_downloads legacy
    WHERE legacy.source_id=r.preview_id AND legacy.device_id='legacy' AND legacy.kind='image'
      AND legacy.source_revision='' AND legacy.hash=r.hash AND legacy.size=r.size
) AND NOT EXISTS (
    SELECT 1 FROM message_previews local JOIN device_downloads retained ON retained.source_id=local.id
    WHERE local.message_id=r.message_id AND local.url=r.url AND retained.device_id='legacy' AND retained.kind='image'
      AND retained.source_revision='' AND retained.hash=r.hash AND retained.size=r.size
)
ON CONFLICT DO NOTHING;
