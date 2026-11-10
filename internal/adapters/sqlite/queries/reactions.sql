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
 SELECT matching.*,row_number() OVER(PARTITION BY actor ORDER BY occurred_at DESC,provider_id DESC,fingerprint DESC) AS position FROM matching
)
SELECT ordinal,id,account_id,address,kind,actor,provider_id,archive_scope,archive_id,target_id,target_scope,target_author,fingerprint,occurred_at,payload,command_id
FROM ranked WHERE position=1 ORDER BY ordinal;

-- name: FindReactionObservation :many
SELECT * FROM message_reaction_observations WHERE account_id=sqlc.arg(account_id) AND address=sqlc.arg(address) AND actor=sqlc.arg(actor)
AND ((CAST(sqlc.arg(provider_id) AS TEXT)<>'' AND provider_id=sqlc.arg(provider_id)) OR
(CAST(sqlc.arg(archive_id) AS TEXT)<>'' AND archive_scope=sqlc.arg(archive_scope) AND archive_id=sqlc.arg(archive_id))) LIMIT 3;

-- name: InsertReactionObservation :exec
INSERT INTO message_reaction_observations(id,account_id,address,kind,actor,provider_id,archive_scope,archive_id,target_id,target_scope,target_author,fingerprint,occurred_at,payload,command_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);

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
