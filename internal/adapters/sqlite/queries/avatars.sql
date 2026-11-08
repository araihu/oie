-- name: FindAvatar :one
SELECT * FROM avatars WHERE account_id=? AND address=?;
-- name: ListAvatars :many
SELECT * FROM avatars WHERE account_id=? AND id>? ORDER BY id LIMIT ?;
-- name: InsertAvatar :exec
INSERT INTO avatars(id,revision_id,account_id,address,payload) VALUES(?,?,?,?,?);
-- name: UpdateAvatar :exec
UPDATE avatars SET revision_id=?,payload=? WHERE id=?;
-- name: InsertAvatarRevision :exec
INSERT INTO avatar_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?);
-- name: ListAvatarTargets :many
SELECT a.address AS address,'account' AS kind FROM accounts AS a WHERE a.id=sqlc.arg(owner_id)
UNION SELECT c.address AS address,CAST(COALESCE(json_extract(c.payload,'$.Kind'),'direct') AS TEXT) AS kind FROM conversations AS c WHERE c.account_id=sqlc.arg(owner_id)
ORDER BY address;
