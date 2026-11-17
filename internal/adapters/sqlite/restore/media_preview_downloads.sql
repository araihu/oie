SELECT local.id, source.payload, COALESCE(thumbnail.hash,''), COALESCE(favicon.hash,'')
FROM message_previews local
JOIN backup.message_previews source ON source.message_id=local.message_id AND source.url=local.url
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
);
