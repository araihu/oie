-- name: ReactionObservationsForTargets :many
-- Start from references or commands so each lookup reads only matching observations.
WITH candidates AS (
 SELECT o.ordinal
 FROM json_each(CAST(sqlc.arg(references) AS TEXT)) ref
 CROSS JOIN message_reaction_observations o
 WHERE o.account_id=sqlc.arg(account_id) AND o.address=sqlc.arg(address)
 AND o.target_id=json_extract(ref.value,'$.id')
 AND (o.target_scope<>'' OR json_extract(ref.value,'$.unique')=1)
 AND (o.target_scope='' OR json_extract(ref.value,'$.scope')=o.target_scope)
 AND (o.target_author='' OR json_extract(ref.value,'$.author')=o.target_author)
 UNION
 SELECT o.ordinal
 FROM message_reaction_commands command
 CROSS JOIN message_reaction_observations o
 WHERE command.message_id=sqlc.arg(message_id) AND o.command_id=command.id AND o.command_id<>''
), matching AS (
SELECT o.* FROM candidates
CROSS JOIN message_reaction_observations o ON o.ordinal=candidates.ordinal
LEFT JOIN message_reaction_commands command ON command.id=o.command_id
WHERE o.account_id=sqlc.arg(account_id) AND o.address=sqlc.arg(address) AND o.kind=sqlc.arg(kind)
AND (command.id IS NULL OR command.state<>'failed')
AND (o.kind<>'room' OR json_extract(o.payload,'$.IsOwn')=1 OR o.room_epoch=sqlc.arg(room_epoch))
AND (o.provider_id='' OR NOT EXISTS(
 SELECT 1 FROM message_reaction_observations conflict
 WHERE conflict.account_id=o.account_id AND conflict.address=o.address AND conflict.actor=o.actor
 AND conflict.provider_id=o.provider_id AND conflict.fingerprint<>o.fingerprint))
AND (o.archive_id='' OR NOT EXISTS(
 SELECT 1 FROM message_reaction_observations conflict
 WHERE conflict.account_id=o.account_id AND conflict.address=o.address AND conflict.actor=o.actor
 AND conflict.archive_scope=o.archive_scope AND conflict.archive_id=o.archive_id
 AND conflict.fingerprint<>o.fingerprint))
), ranked AS (
 SELECT matching.*,row_number() OVER(PARTITION BY actor ORDER BY occurred_at DESC,
 (archive_id<>'') DESC,archive_order DESC,
 CASE WHEN kind='room' AND archive_id='' THEN ordinal ELSE 0 END DESC,
 provider_id DESC,fingerprint DESC) AS position FROM matching
)
SELECT ordinal,id,account_id,address,kind,actor,provider_id,archive_scope,archive_id,target_id,target_scope,target_author,fingerprint,occurred_at,payload,command_id,archive_order
FROM ranked WHERE position=1 ORDER BY ordinal;

-- name: FindReactionObservation :many
SELECT * FROM message_reaction_observations WHERE account_id=sqlc.arg(account_id) AND address=sqlc.arg(address) AND actor=sqlc.arg(actor)
AND fingerprint=sqlc.arg(fingerprint)
AND ((CAST(sqlc.arg(provider_id) AS TEXT)<>'' AND provider_id=sqlc.arg(provider_id)) OR
(CAST(sqlc.arg(archive_id) AS TEXT)<>'' AND archive_scope=sqlc.arg(archive_scope) AND archive_id=sqlc.arg(archive_id)) OR
(CAST(sqlc.arg(command_id) AS TEXT)<>'' AND command_id=sqlc.arg(command_id))) ORDER BY ordinal LIMIT 4;

-- name: InsertReactionObservation :exec
INSERT INTO message_reaction_observations(id,account_id,address,kind,actor,provider_id,archive_scope,archive_id,target_id,target_scope,target_author,fingerprint,occurred_at,payload,command_id,archive_order,room_epoch)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);

-- name: ArchivedReactionNeighbors :one
SELECT CAST(COALESCE(MIN(archive_order),0) AS INTEGER) AS first_order,
 CAST(COALESCE(MAX(archive_order),0) AS INTEGER) AS last_order,
 CAST(COALESCE(MAX(CASE WHEN archive_id IN (SELECT value FROM json_each(CAST(sqlc.arg(earlier) AS TEXT))) THEN archive_order END),0) AS INTEGER) AS earlier_order,
 CAST(COALESCE(MIN(CASE WHEN archive_id IN (SELECT value FROM json_each(CAST(sqlc.arg(later) AS TEXT))) THEN archive_order END),0) AS INTEGER) AS later_order,
 COUNT(CASE WHEN archive_id IN (SELECT value FROM json_each(CAST(sqlc.arg(earlier) AS TEXT))) THEN 1 END) AS earlier_count,
 COUNT(CASE WHEN archive_id IN (SELECT value FROM json_each(CAST(sqlc.arg(later) AS TEXT))) THEN 1 END) AS later_count
FROM message_reaction_observations
WHERE account_id=sqlc.arg(account_id) AND archive_scope=sqlc.arg(archive_scope)
AND occurred_at=sqlc.arg(occurred_at) AND archive_id<>'' AND ordinal<>sqlc.arg(excluded_ordinal);

-- name: ShiftArchivedReactions :exec
UPDATE message_reaction_observations SET archive_order=archive_order+1
WHERE account_id=sqlc.arg(account_id) AND archive_scope=sqlc.arg(archive_scope)
AND occurred_at=sqlc.arg(occurred_at) AND archive_id<>'' AND archive_order>=sqlc.arg(placement)
AND ordinal<>sqlc.arg(excluded_ordinal);

-- name: DeleteReactionObservation :exec
DELETE FROM message_reaction_observations WHERE ordinal=?;

-- name: ReconcileReactionObservation :exec
UPDATE message_reaction_observations SET provider_id=?,archive_scope=?,archive_id=?,occurred_at=?,archive_order=?,payload=?,command_id=?
WHERE ordinal=?;

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

-- name: GetRoomReactionPrivacy :one
SELECT public,identity_mode,epoch FROM room_reaction_privacy WHERE account_id=? AND address=?;

-- name: SetRoomReactionPrivacy :execrows
INSERT INTO room_reaction_privacy(account_id,address,public,identity_mode) VALUES(?,?,?,?)
ON CONFLICT(account_id,address) DO UPDATE SET
 public=excluded.public,
 identity_mode=CASE WHEN excluded.identity_mode<>'' THEN excluded.identity_mode ELSE room_reaction_privacy.identity_mode END,
 epoch=room_reaction_privacy.epoch+CASE WHEN room_reaction_privacy.identity_mode<>'' AND excluded.identity_mode<>'' AND room_reaction_privacy.identity_mode<>excluded.identity_mode THEN 1 ELSE 0 END
WHERE room_reaction_privacy.public<>excluded.public OR (excluded.identity_mode<>'' AND room_reaction_privacy.identity_mode<>excluded.identity_mode);

-- name: ClearRoomReactionVisibility :exec
UPDATE room_reaction_privacy SET public=0 WHERE account_id=? AND address=? AND public<>0;

-- name: ResetRoomReactionPrivacy :exec
UPDATE room_reaction_privacy SET public=0 WHERE account_id=? AND public<>0;

-- name: ResetAllRoomReactionPrivacy :exec
UPDATE room_reaction_privacy SET public=0 WHERE public<>0;
