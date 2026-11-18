WITH candidates AS (
SELECT local.id, source.payload, COALESCE(thumbnail.hash,'') AS thumbnail, COALESCE(favicon.hash,'') AS favicon,
       row_number() OVER (PARTITION BY local.id ORDER BY (thumbnail.hash IS NOT NULL)+(favicon.hash IS NOT NULL) DESC,
                          source.updated_at DESC,source.id DESC) AS priority
FROM message_previews local
JOIN restore_media_targets target ON target.message_id=local.message_id
JOIN backup.message_previews source ON source.message_id=target.id AND source.url=local.url
LEFT JOIN restore_media_files thumbnail ON thumbnail.name='media/images/'||json_extract(source.payload,'$.ImageHash')
LEFT JOIN restore_media_files favicon ON favicon.name='media/images/'||json_extract(source.payload,'$.FaviconHash')
WHERE (
    local.download_state IN ('not_downloaded','failed') AND COALESCE(json_extract(local.payload,'$.FileHash'),'')=''
    OR local.download_state='ready' AND (
        COALESCE(json_extract(local.payload,'$.ImageHash'),'')='' AND thumbnail.hash IS NOT NULL
        OR COALESCE(json_extract(local.payload,'$.FaviconHash'),'')='' AND favicon.hash IS NOT NULL
    )
)
AND EXISTS (
    SELECT 1 FROM restore_downloads f
    WHERE f.kind='image' AND f.source_id=local.id AND f.hash=json_extract(source.payload,'$.FileHash')
))
SELECT id,payload,thumbnail,favicon FROM candidates WHERE priority=1;
