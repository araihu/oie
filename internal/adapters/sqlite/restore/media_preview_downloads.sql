SELECT local.id, json_extract(source.payload,'$.FileHash'), json_extract(source.payload,'$.FileMimeType'),
       json_extract(source.payload,'$.FileSize'), COALESCE(json_extract(source.payload,'$.VerifiedAlgorithm'),'')
FROM message_previews local
JOIN backup.message_previews source ON source.message_id=local.message_id AND source.url=local.url
WHERE local.download_state IN ('not_downloaded','failed') AND COALESCE(json_extract(local.payload,'$.FileHash'),'')=''
AND EXISTS (
    SELECT 1 FROM restore_downloads f
    WHERE f.kind='image' AND f.source_id=local.id AND f.hash=json_extract(source.payload,'$.FileHash')
);
