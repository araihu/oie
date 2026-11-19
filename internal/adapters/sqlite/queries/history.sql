-- name: GetArchiveAlias :one
SELECT message_id FROM archive_aliases WHERE account_id=? AND scope=? AND archive_id=?;
-- name: InsertArchiveAlias :exec
INSERT INTO archive_aliases(account_id,scope,archive_id,message_id) VALUES(?,?,?,?);
-- name: GetArchiveCheckpoint :one
SELECT after_id FROM archive_checkpoints WHERE account_id=? AND scope=?;
-- name: PutArchiveCheckpoint :exec
INSERT INTO archive_checkpoints(account_id,scope,after_id) VALUES(?,?,?)
ON CONFLICT(account_id,scope) DO UPDATE SET after_id=excluded.after_id;
-- name: GetHistoryStatus :one
SELECT payload FROM history_status WHERE account_id=?;
-- name: PutHistoryStatus :exec
INSERT INTO history_status(account_id,payload) VALUES(?,?) ON CONFLICT(account_id) DO UPDATE SET payload=excluded.payload;
-- name: GetHistoryCounts :one
SELECT CAST((SELECT COUNT(*) FROM messages AS m WHERE m.account_id=sqlc.arg(owner_id) AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)) AS INTEGER) AS retained_messages,
CAST((SELECT COUNT(*) FROM conversations AS c WHERE c.account_id=sqlc.arg(owner_id)) AS INTEGER) AS conversations;

-- name: PutGuardedArchiveCheckpoint :execrows
INSERT INTO archive_checkpoints(account_id,scope,after_id)
SELECT sqlc.arg(account_id),sqlc.arg(scope),sqlc.arg(after_id)
WHERE (SELECT g.archive_revision+COALESCE(a.revision,0)
 FROM global_retention g LEFT JOIN account_archive_retention a ON a.account_id=sqlc.arg(account_id)
 WHERE g.singleton=1)=sqlc.arg(retention_revision)
ON CONFLICT(account_id,scope) DO UPDATE SET after_id=excluded.after_id;

-- name: FindVerifiedRoomOccurrence :many
SELECT DISTINCT CAST(COALESCE(held.original_id,r.message_id) AS TEXT) AS message_id FROM message_references r
JOIN messages m ON m.id=r.message_id
JOIN conversations c ON c.id=m.conversation_id AND c.account_id=m.account_id
LEFT JOIN room_archive_echo_links held ON held.echo_id=m.id AND held.room_uid=r.remote_id
WHERE m.account_id=sqlc.arg(account_id) AND c.address=sqlc.arg(room_address)
  AND json_extract(c.payload,'$.Kind')='room' AND r.scope='room:'||c.address
  AND r.remote_id=sqlc.arg(remote_id) AND instr(r.author,c.address||'/')=1
LIMIT 2;

-- name: GetLegacyRoomArchiveSource :one
SELECT a.message_id FROM archive_aliases a
JOIN legacy_room_archive_uids old ON old.message_id=a.message_id AND old.archive_id=a.archive_id
WHERE a.account_id=? AND a.scope=? AND a.archive_id=?;

-- name: RoomArchiveEchoOwner :one
SELECT p.original_id FROM room_archive_echo_links p WHERE p.echo_id=sqlc.arg(echo_id)
UNION ALL
SELECT '' FROM disputed_room_archive_echoes d WHERE d.message_id=sqlc.arg(echo_id);

-- name: DisputeRoomArchiveEcho :exec
INSERT OR IGNORE INTO disputed_room_archive_echoes(message_id) SELECT echo_id FROM room_archive_echo_links WHERE echo_id=?;

-- name: IsRoomArchiveEchoOf :one
SELECT EXISTS(SELECT 1 FROM room_archive_echo_links WHERE echo_id=? AND original_id=? AND room_uid=?);

-- name: LinkRoomArchiveEcho :one
INSERT OR IGNORE INTO room_archive_echo_links(echo_id,original_id,account_id,room_address,room_uid)
SELECT p.echo_id,p.original_id,p.account_id,p.room_address,p.room_uid FROM room_archive_echo_pairs p
WHERE p.account_id=sqlc.arg(account_id) AND p.room_address=sqlc.arg(room_address) AND p.room_uid=sqlc.arg(room_uid)
RETURNING echo_id;

-- name: RestoreRoomArchiveEchoLinks :exec
INSERT OR IGNORE INTO room_archive_echo_links(echo_id,original_id,account_id,room_address,room_uid)
SELECT p.echo_id,p.original_id,p.account_id,p.room_address,p.room_uid FROM room_archive_echo_pairs p;

-- name: MatchExcludedXMPPOccurrence :one
-- Only an observed key may teach us another deletion key. The opposite
-- form of a room UID can suppress a replay without becoming a new fact.
WITH markers AS (
 SELECT e.* FROM xmpp_message_exclusions e JOIN accounts a ON a.address=e.account AND a.protocol='xmpp'
 WHERE a.id=sqlc.arg(account_id) AND e.chat=sqlc.arg(chat) AND e.kind=sqlc.arg(kind)
 AND e.remote_id IN (sqlc.arg(archive_id),sqlc.arg(stanza_id))
)
SELECT CAST(CASE WHEN EXISTS (
 SELECT 1 FROM markers e WHERE (e.key_type='mam' AND e.authority=sqlc.arg(archive_scope) AND e.remote_id=sqlc.arg(archive_id))
 OR (e.key_type='stanza-id' AND e.authority=e.chat AND e.remote_id=sqlc.arg(stanza_id)
     AND NOT (sqlc.arg(archive_scope)=e.chat AND sqlc.arg(archive_id)=sqlc.arg(stanza_id)))
) THEN 'observed' WHEN EXISTS (
 SELECT 1 FROM markers e WHERE e.kind='room' AND e.authority=e.chat
 AND NOT EXISTS (SELECT 1 FROM distinct_room_exclusion_keys d WHERE d.account=e.account AND d.chat=e.chat
                 AND d.key_type=e.key_type AND d.remote_id=e.remote_id)
 AND ((e.key_type='mam' AND e.remote_id=sqlc.arg(stanza_id)
       AND (sqlc.arg(archive_scope)<>e.chat OR sqlc.arg(archive_id)=sqlc.arg(stanza_id)))
   OR (e.key_type='stanza-id' AND sqlc.arg(archive_scope)=e.chat AND e.remote_id=sqlc.arg(archive_id)
       AND (sqlc.arg(stanza_id)='' OR sqlc.arg(stanza_id)=sqlc.arg(archive_id))))
) THEN 'equivalent' ELSE '' END AS TEXT) AS match_kind;

-- name: RememberExcludedXMPPKey :exec
INSERT OR IGNORE INTO xmpp_message_exclusions(account,chat,kind,key_type,authority,remote_id)
SELECT a.address,sqlc.arg(chat),sqlc.arg(kind),sqlc.arg(key_type),sqlc.arg(authority),sqlc.arg(remote_id)
FROM accounts a WHERE a.id=sqlc.arg(account_id) AND a.protocol='xmpp';

-- name: RoomSourceAcceptsIdentity :one
-- A room UID locates an occurrence. Confirm that its occupant and body still
-- agree before attaching incoming metadata to the retained source.
SELECT EXISTS (
 SELECT 1 FROM messages m JOIN accounts a ON a.id=m.account_id
 WHERE m.id=sqlc.arg(message_id)
 AND (COALESCE(NULLIF(json_extract(m.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(m.payload,'$.OriginalText')))=sqlc.arg(source_hash)
   OR (m.is_own=1 AND oie_attachment_wire_matches(json_extract(m.payload,'$.OriginalText'),json_extract(m.payload,'$.AttachmentWireText'),json_extract(m.payload,'$.WireBodySHA256'),sqlc.arg(wire_body))=1))
 AND (m.is_own=1 AND m.id=m.provider_id OR sqlc.arg(author)=sqlc.arg(room_address)||'/'||json_extract(m.payload,'$.Sender.DisplayName'))
 AND (sqlc.arg(personal_address)='' OR m.is_own=0 OR sqlc.arg(personal_address)=a.address)
 AND NOT EXISTS (SELECT 1 FROM message_sender_identities i WHERE i.message_id=m.id AND
       (sqlc.arg(is_own)=1 AND i.personal_address<>a.address OR sqlc.arg(personal_address)<>'' AND i.personal_address<>sqlc.arg(personal_address)))
 AND NOT EXISTS (SELECT 1 FROM message_references r WHERE r.message_id=m.id AND r.scope='room:'||sqlc.arg(room_address)
       AND (r.remote_id<>sqlc.arg(uid) OR r.author<>sqlc.arg(author)))
);

-- name: RememberDistinctRoomExclusionKey :exec
INSERT OR IGNORE INTO distinct_room_exclusion_keys(account,chat,key_type,remote_id)
SELECT a.address,sqlc.arg(chat),sqlc.arg(key_type),sqlc.arg(remote_id)
FROM accounts a WHERE a.id=sqlc.arg(account_id) AND a.protocol='xmpp';
