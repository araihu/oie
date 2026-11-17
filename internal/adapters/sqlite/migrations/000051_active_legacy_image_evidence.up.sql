-- A named copy seen during restore is weaker evidence than a local claim or
-- retirement. Keep it separately so an earlier exact admission still works.
CREATE TABLE active_legacy_image_claims (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    hash TEXT NOT NULL,
    size INTEGER NOT NULL CHECK(size >= 0),
    PRIMARY KEY(message_id,url,source_revision,hash,size)
) WITHOUT ROWID;

INSERT INTO active_legacy_image_claims(message_id,url,source_revision,hash,size)
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
