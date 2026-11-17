-- name: RemoveBackupRoomPasswords :exec
DELETE FROM room_invitation_credentials;

-- name: ListBackupMedia :many
SELECT 'avatars' AS subdir, CAST(hash || '.png' AS TEXT) AS filename, hash, -1 AS size
FROM (
    SELECT CAST(json_extract(payload, '$.ImageHash') AS TEXT) AS hash FROM avatars
) WHERE hash <> ''
UNION
SELECT 'images', hash, hash, size FROM media_files
UNION
SELECT 'downloads', hash, hash, size FROM device_downloads WHERE kind='file' AND hash <> ''
UNION
SELECT 'staged', id, sha256, size FROM staged_attachments
ORDER BY 1,2;

-- name: SummarizeBackup :one
SELECT
  (SELECT COUNT(*) FROM accounts) AS accounts,
  (SELECT COUNT(*) FROM conversations) AS conversations,
  (SELECT COUNT(*) FROM messages WHERE NOT EXISTS
    (SELECT 1 FROM message_canonical_aliases WHERE alias_id=messages.id)) AS messages,
  (SELECT COUNT(*) FROM outbox WHERE state IN ('queued','dispatching','unknown')) AS pending_sends,
  (SELECT COUNT(*) FROM room_invitation_credentials) AS room_passwords;
