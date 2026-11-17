-- A named membership prevents a backup of the global original from reopening
-- its storage outside the device policy, even when this backup omits media.
INSERT INTO legacy_image_claims(message_id,url,source_revision,hash,size)
SELECT p.message_id,p.url,named.source_revision,named.hash,named.size
FROM backup.device_downloads named JOIN backup.message_previews p ON p.id=named.source_id
JOIN restore_messages selected ON selected.id=p.message_id
JOIN messages retained ON retained.id=selected.id AND retained.account_id=p.account_id AND retained.conversation_id=p.conversation_id
LEFT JOIN message_content_heads head ON head.canonical_id=retained.id
WHERE named.device_id NOT IN ('','legacy') AND named.kind='image' AND p.download_state='ready'
  AND json_extract(p.payload,'$.Kind')='image'
  AND json_extract(p.payload,'$.FileHash')=named.hash AND json_extract(p.payload,'$.FileSize')=named.size
  AND length(named.hash)=64 AND named.hash NOT GLOB '*[^0-9a-f]*' AND named.size>0
  AND oie_quote_preview_allowed(COALESCE(head.payload,retained.payload),p.url)
  AND NOT EXISTS (
    SELECT 1 FROM backup.device_downloads legacy WHERE legacy.device_id='legacy' AND legacy.kind='image'
      AND legacy.source_id=p.id AND legacy.source_revision=named.source_revision
      AND legacy.hash=named.hash AND legacy.size=named.size
  )
  AND NOT EXISTS (
    SELECT 1 FROM message_previews local JOIN device_downloads legacy ON legacy.source_id=local.id
    WHERE local.message_id=p.message_id AND local.url=p.url AND legacy.device_id='legacy' AND legacy.kind='image'
      AND legacy.source_revision=named.source_revision AND legacy.hash=named.hash AND legacy.size=named.size
  )
ON CONFLICT DO NOTHING;
