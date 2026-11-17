INSERT INTO active_legacy_image_claims
SELECT c.* FROM backup.active_legacy_image_claims c JOIN restore_messages m ON m.id=c.message_id
JOIN messages retained ON retained.id=m.id
LEFT JOIN message_content_heads head ON head.canonical_id=retained.id
WHERE oie_quote_preview_allowed(COALESCE(head.payload,retained.payload),c.url)
AND length(c.hash)=64 AND c.hash NOT GLOB '*[^0-9a-f]*' AND c.size>0
AND NOT EXISTS (
    SELECT 1 FROM backup.message_previews p JOIN backup.device_downloads legacy ON legacy.source_id=p.id
    WHERE p.message_id=c.message_id AND p.url=c.url AND legacy.device_id='legacy' AND legacy.kind='image'
      AND legacy.source_revision=c.source_revision AND legacy.hash=c.hash AND legacy.size=c.size
)
ON CONFLICT DO NOTHING;
