-- name: GetRoomIntent :one
SELECT * FROM room_intents WHERE conversation_id=?;

-- name: ListRoomIntents :many
SELECT * FROM room_intents ORDER BY conversation_id;

-- name: PutRoomIntent :exec
INSERT INTO room_intents(conversation_id,account_id,id,revision_id,nickname,active,last_error_code,updated_at,creation_name)
VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(conversation_id) DO UPDATE SET observation_fingerprint='',revision_id=excluded.revision_id,nickname=excluded.nickname,active=excluded.active,last_error_code=excluded.last_error_code,updated_at=excluded.updated_at,creation_name=excluded.creation_name;

-- name: SetRoomObservation :exec
UPDATE room_intents SET observation_fingerprint=sqlc.arg(observation_fingerprint),
creation_name=CASE WHEN CAST(sqlc.arg(joined) AS INTEGER)=1 THEN NULL ELSE creation_name END
WHERE conversation_id=sqlc.arg(conversation_id) AND revision_id=sqlc.arg(revision_id);
