-- Existing named memberships already prevent reimporting the same global
-- original. Keep that evidence when exporting without bytes or expiring it.
INSERT INTO legacy_image_claims(message_id,url,source_revision,hash,size)
SELECT p.message_id,p.url,named.source_revision,named.hash,named.size
FROM device_downloads named JOIN message_previews p ON p.id=named.source_id
WHERE named.device_id NOT IN ('','legacy') AND named.kind='image' AND p.download_state='ready'
  AND json_extract(p.payload,'$.Kind')='image'
  AND json_extract(p.payload,'$.FileHash')=named.hash AND json_extract(p.payload,'$.FileSize')=named.size
  AND length(named.hash)=64 AND named.hash NOT GLOB '*[^0-9a-f]*' AND named.size>0
  AND NOT EXISTS (
    SELECT 1 FROM device_downloads legacy WHERE legacy.device_id='legacy' AND legacy.kind='image'
      AND legacy.source_id=p.id AND legacy.source_revision=named.source_revision
      AND legacy.hash=named.hash AND legacy.size=named.size
  )
ON CONFLICT DO NOTHING;
