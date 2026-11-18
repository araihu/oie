-- Metadata can outlive a deleted file while garbage collection is pending.
-- Publish only bytes with a surviving reference, not orphaned cache entries.
WITH refs(name,hash) AS (
    SELECT 'media/avatars/'||json_extract(payload,'$.ImageHash')||'.png',json_extract(payload,'$.ImageHash')
    FROM avatars
    UNION
    SELECT 'media/images/'||r.value,r.value
    FROM message_previews p, json_each(json_array(
        json_extract(p.payload,'$.ImageHash'),json_extract(p.payload,'$.FaviconHash'),json_extract(p.payload,'$.FileHash')
    )) r WHERE r.value<>''
    UNION
    SELECT 'media/downloads/'||hash,hash FROM device_downloads WHERE kind='file'
    UNION
    SELECT 'media/staged/'||id,sha256 FROM staged_attachments
)
SELECT refs.name,refs.hash
FROM json_each(CAST(@media_files AS TEXT)) bytes
JOIN refs ON refs.name=json_extract(bytes.value,'$.name') AND refs.hash=json_extract(bytes.value,'$.sha256');
