-- name: ListRoomBannedNicknames :many
SELECT address, nickname FROM room_ban_nicknames WHERE conversation_id = ?;

-- name: PutRoomBannedNickname :exec
INSERT INTO room_ban_nicknames (conversation_id, address, nickname) VALUES (?, ?, ?)
ON CONFLICT (conversation_id, address) DO UPDATE SET nickname = excluded.nickname;

-- name: RemoveRoomBannedNickname :exec
DELETE FROM room_ban_nicknames WHERE conversation_id = ? AND address = ?;
