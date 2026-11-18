INSERT INTO legacy_image_claims
SELECT c.* FROM backup.legacy_image_claims c JOIN restore_messages m ON m.id=c.message_id
-- A retained global membership is an independent destination choice.
WHERE NOT EXISTS (
    SELECT 1 FROM message_previews local JOIN device_downloads retained ON retained.source_id=local.id
    WHERE local.message_id=c.message_id AND local.url=c.url AND retained.device_id='legacy' AND retained.kind='image'
      AND retained.source_revision=c.source_revision AND retained.hash=c.hash AND retained.size=c.size
)
ON CONFLICT DO NOTHING;
