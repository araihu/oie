-- name: ReactionObservationsForTargets :many
WITH matching AS (
SELECT o.* FROM message_reaction_observations o
LEFT JOIN message_reaction_commands command ON command.id=o.command_id
WHERE o.account_id=sqlc.arg(account_id) AND o.address=sqlc.arg(address) AND o.kind=sqlc.arg(kind)
AND (command.message_id=sqlc.arg(message_id) OR EXISTS(SELECT 1 FROM json_each(CAST(sqlc.arg(references) AS TEXT)) ref
 WHERE json_extract(ref.value,'$.id')=o.target_id
 AND (o.target_scope<>'' OR json_extract(ref.value,'$.unique')=1)
 AND (o.target_scope='' OR json_extract(ref.value,'$.scope')=o.target_scope)
 AND (o.target_author='' OR json_extract(ref.value,'$.author')=o.target_author)))
AND (command.id IS NULL OR command.state<>'failed')
AND NOT EXISTS(SELECT 1 FROM message_reaction_observations conflict
 WHERE conflict.account_id=o.account_id AND conflict.address=o.address AND conflict.actor=o.actor
 AND ((o.provider_id<>'' AND conflict.provider_id=o.provider_id) OR (o.archive_id<>'' AND conflict.archive_scope=o.archive_scope AND conflict.archive_id=o.archive_id))
 AND conflict.fingerprint<>o.fingerprint)
), ranked AS (
 SELECT matching.*,row_number() OVER(PARTITION BY actor ORDER BY occurred_at DESC,
 (archive_id<>'') DESC,archive_order DESC,provider_id DESC,fingerprint DESC) AS position FROM matching
 WHERE kind<>'room' OR archive_id<>'' OR command_id<>'' OR provider_id LIKE 'room-reaction:%'
 OR NOT EXISTS(SELECT 1 FROM matching stable WHERE stable.actor=matching.actor
  AND (stable.archive_id<>'' OR stable.provider_id LIKE 'room-reaction:%'))
)
SELECT ordinal,id,account_id,address,kind,actor,provider_id,archive_scope,archive_id,target_id,target_scope,target_author,fingerprint,occurred_at,payload,command_id,archive_order
FROM ranked WHERE position=1 ORDER BY ordinal;

-- name: FindReactionObservation :many
SELECT * FROM message_reaction_observations WHERE account_id=sqlc.arg(account_id) AND address=sqlc.arg(address) AND actor=sqlc.arg(actor)
AND fingerprint=sqlc.arg(fingerprint)
AND ((CAST(sqlc.arg(provider_id) AS TEXT)<>'' AND provider_id=sqlc.arg(provider_id)) OR
(CAST(sqlc.arg(archive_id) AS TEXT)<>'' AND archive_scope=sqlc.arg(archive_scope) AND archive_id=sqlc.arg(archive_id)) OR
(CAST(sqlc.arg(command_id) AS TEXT)<>'' AND command_id=sqlc.arg(command_id) AND provider_id=command_id)) LIMIT 4;

-- name: InsertReactionObservation :exec
INSERT INTO message_reaction_observations(id,account_id,address,kind,actor,provider_id,archive_scope,archive_id,target_id,target_scope,target_author,fingerprint,occurred_at,payload,command_id,archive_order)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);

-- name: ArchivedReactionPlacement :one
SELECT CAST(COALESCE(
 MIN(CASE WHEN archive_id IN (SELECT value FROM json_each(CAST(sqlc.arg(later) AS TEXT))) THEN archive_order END),
 MAX(CASE WHEN archive_id IN (SELECT value FROM json_each(CAST(sqlc.arg(earlier) AS TEXT))) THEN archive_order END)+1,
 CASE WHEN CAST(sqlc.arg(prepend) AS INTEGER)=1 THEN MIN(archive_order)-1 ELSE MAX(archive_order)+1 END,
 1
) AS INTEGER) AS placement
FROM message_reaction_observations
WHERE account_id=sqlc.arg(account_id) AND archive_scope=sqlc.arg(archive_scope)
AND occurred_at=sqlc.arg(occurred_at) AND archive_id<>'';

-- name: ShiftArchivedReactions :exec
UPDATE message_reaction_observations SET archive_order=archive_order+1
WHERE account_id=sqlc.arg(account_id) AND archive_scope=sqlc.arg(archive_scope)
AND occurred_at=sqlc.arg(occurred_at) AND archive_id<>'' AND archive_order>=sqlc.arg(placement);

-- name: DeleteReactionObservation :exec
DELETE FROM message_reaction_observations WHERE ordinal=?;

-- name: UpdateArchivedReactionIdentity :exec
UPDATE message_reaction_observations SET provider_id=?,payload=?,command_id=? WHERE ordinal=?;

-- name: GetMessageReactionCommand :one
SELECT * FROM message_reaction_commands WHERE id=?;

-- name: GetActiveMessageReaction :one
SELECT * FROM message_reaction_commands WHERE message_id=? AND state IN ('queued','dispatching','unknown') LIMIT 1;

-- name: InsertMessageReactionCommand :exec
INSERT INTO message_reaction_commands(id,revision_id,message_id,account_id,conversation_id,state,payload,next_attempt_at) VALUES(?,?,?,?,?,?,?,?);

-- name: UpdateMessageReactionCommand :exec
UPDATE message_reaction_commands SET revision_id=?,state=?,payload=?,next_attempt_at=? WHERE id=?;

-- name: PendingMessageReactionCommands :many
SELECT * FROM message_reaction_commands WHERE state='queued' AND next_attempt_at<=? ORDER BY next_attempt_at,id LIMIT 32;

-- name: InterruptedMessageReactionCommands :many
SELECT * FROM message_reaction_commands WHERE state='dispatching' ORDER BY id;

-- name: ReactionActorProfile :one
SELECT
 CAST(COALESCE((SELECT co.display_name FROM contacts co WHERE co.account_id=sqlc.arg(account_id) AND co.address=sqlc.arg(address) AND co.active=1),'') AS TEXT) AS display_name,
 CAST(COALESCE((SELECT json_extract(av.payload,'$.ImageHash') FROM avatars av WHERE av.account_id=sqlc.arg(account_id) AND av.address=sqlc.arg(address)),'') AS TEXT) AS avatar_hash;

-- name: RoomReactionPrivacy :one
SELECT CAST(COALESCE((SELECT public FROM room_reaction_privacy
 WHERE account_id=sqlc.arg(account_id) AND address=sqlc.arg(address)),0) AS INTEGER);

-- name: SetRoomReactionPrivacy :execrows
INSERT INTO room_reaction_privacy(account_id,address,public) VALUES(?,?,?)
ON CONFLICT(account_id,address) DO UPDATE SET public=excluded.public WHERE public<>excluded.public;


-- name: ResetRoomReactionPrivacy :exec
DELETE FROM room_reaction_privacy WHERE account_id=?;

-- name: ResetAllRoomReactionPrivacy :exec
DELETE FROM room_reaction_privacy;
