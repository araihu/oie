-- name: GetRoomIntent :one
SELECT * FROM room_intents WHERE conversation_id=?;

-- name: ListRoomIntents :many
SELECT * FROM room_intents ORDER BY conversation_id;

-- name: PutRoomIntent :exec
INSERT INTO room_intents(conversation_id,account_id,id,revision_id,nickname,active,last_error_code,updated_at)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(conversation_id) DO UPDATE SET observation_fingerprint='',revision_id=excluded.revision_id,nickname=excluded.nickname,active=excluded.active,last_error_code=excluded.last_error_code,updated_at=excluded.updated_at;

-- name: SetRoomObservation :exec
UPDATE room_intents SET observation_fingerprint=? WHERE conversation_id=? AND revision_id=?;
