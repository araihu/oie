INSERT INTO staged_attachments SELECT f.* FROM backup.staged_attachments f JOIN restore_chats x ON x.id=f.conversation_id
WHERE EXISTS(SELECT 1 FROM recovered_sends s JOIN json_each(s.payload,'$.Attachments') a
    WHERE s.conversation_id=f.conversation_id AND json_extract(a.value,'$.ID')=f.id)
OR EXISTS (SELECT 1 FROM messages m JOIN restore_messages x ON x.id=m.id
    JOIN json_each(m.payload,'$.Attachments') a
    WHERE m.conversation_id=f.conversation_id AND json_extract(a.value,'$.ID')=f.id)
ON CONFLICT(id) DO NOTHING;

INSERT INTO message_previews SELECT p.* FROM backup.message_previews p JOIN restore_messages x ON x.id=p.message_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO preview_revisions
SELECT r.* FROM backup.preview_revisions r JOIN backup.message_previews b ON b.id=r.entity_id
JOIN message_previews p ON p.id=b.id AND p.message_id=b.message_id
JOIN restore_messages x ON x.id=b.message_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;

INSERT INTO media_files SELECT f.* FROM backup.media_files f
WHERE EXISTS (SELECT 1 FROM message_previews p JOIN restore_messages x ON x.id=p.message_id
    WHERE f.hash IN (json_extract(p.payload,'$.FileHash'), json_extract(p.payload,'$.ImageHash'), json_extract(p.payload,'$.FaviconHash')))
ON CONFLICT(hash) DO NOTHING;

INSERT INTO download_devices
SELECT DISTINCT d.device_id,d.days,d.generation FROM backup.download_devices d
JOIN backup.device_downloads f ON f.device_id=d.device_id
WHERE EXISTS(SELECT 1 FROM shared_content ref JOIN restore_messages x ON x.id=ref.message_id WHERE ref.id=f.source_id)
   OR EXISTS(SELECT 1 FROM message_previews p JOIN restore_messages x ON x.id=p.message_id WHERE p.id=f.source_id)
ON CONFLICT(device_id) DO NOTHING;

INSERT INTO device_downloads
SELECT f.* FROM backup.device_downloads f JOIN download_devices d ON d.device_id=f.device_id
WHERE EXISTS(SELECT 1 FROM shared_content ref JOIN restore_messages x ON x.id=ref.message_id WHERE ref.id=f.source_id)
   OR EXISTS(SELECT 1 FROM message_previews p JOIN restore_messages x ON x.id=p.message_id WHERE p.id=f.source_id)
ON CONFLICT DO NOTHING;
