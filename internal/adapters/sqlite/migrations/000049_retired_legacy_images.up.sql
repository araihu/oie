-- Older Settings could claim and expire a legacy original without recording
-- a claim. Follow the retained preview branch to recover exact detachments.
WITH RECURSIVE image_history(preview_id,revision_id) AS (
    SELECT id,revision_id FROM message_previews WHERE json_extract(payload,'$.Kind')='image'
    UNION
    SELECT history.preview_id,parent.id
    FROM image_history history
    JOIN preview_revisions child ON child.id=history.revision_id AND child.entity_id=history.preview_id
    JOIN json_each(child.parents) edge ON edge.type='text'
    JOIN preview_revisions parent ON parent.id=edge.value AND parent.entity_id=child.entity_id
)
INSERT INTO legacy_image_claims(message_id,url,source_revision,hash,size)
SELECT preview.message_id,preview.url,'',json_extract(ready.payload,'$.FileHash'),json_extract(ready.payload,'$.FileSize')
FROM image_history history JOIN message_previews preview ON preview.id=history.preview_id
JOIN preview_revisions detached ON detached.id=history.revision_id AND detached.entity_id=preview.id
JOIN preview_revisions ready ON ready.id=json_extract(detached.parents,'$[0]') AND ready.entity_id=preview.id
WHERE json_array_length(detached.parents)=1
  AND json_extract(detached.payload,'$.ID')=preview.id AND json_extract(ready.payload,'$.ID')=preview.id
  AND json_extract(detached.payload,'$.MessageID')=preview.message_id AND json_extract(ready.payload,'$.MessageID')=preview.message_id
  AND json_extract(detached.payload,'$.URL')=preview.url AND json_extract(ready.payload,'$.URL')=preview.url
  AND json_extract(detached.payload,'$.Kind')='image' AND json_extract(ready.payload,'$.Kind')='image'
  AND json_extract(detached.payload,'$.DownloadState')='not_downloaded'
  AND json_extract(detached.payload,'$.FileHash')='' AND json_extract(detached.payload,'$.FileMimeType')=''
  AND json_extract(detached.payload,'$.FileSize')=0 AND json_extract(detached.payload,'$.LastErrorCode')=''
  AND json_extract(ready.payload,'$.DownloadState')='ready'
  AND length(json_extract(ready.payload,'$.FileHash'))=64 AND json_extract(ready.payload,'$.FileHash') NOT GLOB '*[^0-9a-f]*'
  AND json_type(ready.payload,'$.FileSize')='integer' AND json_extract(ready.payload,'$.FileSize')>0
ON CONFLICT DO NOTHING;
