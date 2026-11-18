-- name: LatestOwnMessage :one
SELECT m.* FROM original_transmission_order attempt JOIN messages m ON m.id=attempt.message_id
WHERE attempt.conversation_id=? AND m.conversation_id=attempt.conversation_id AND m.is_own=1
AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
ORDER BY attempt.ordinal DESC LIMIT 1;

-- name: RecordOriginalTransmission :exec
INSERT INTO original_transmission_order(message_id,conversation_id)
SELECT m.id,m.conversation_id FROM messages m WHERE m.id=? AND m.is_own=1
AND EXISTS(SELECT 1 FROM operations op WHERE op.kind='send_message' AND op.resource_id=m.id);

-- name: OriginalSendReceipt :one
SELECT * FROM operations WHERE resource_id=? AND kind='send_message' LIMIT 1;

-- name: GetMessageEditCommand :one
SELECT * FROM message_edit_commands WHERE id=?;

-- name: GetActiveMessageEdit :one
SELECT * FROM message_edit_commands WHERE message_id=? AND state IN ('queued','dispatching','unknown') LIMIT 1;

-- name: InsertMessageEditCommand :exec
INSERT INTO message_edit_commands(id,revision_id,message_id,account_id,conversation_id,state,payload,next_attempt_at) VALUES(?,?,?,?,?,?,?,?);

-- name: UpdateMessageEditCommand :exec
UPDATE message_edit_commands SET revision_id=?,state=?,payload=?,next_attempt_at=?,attempt_count=attempt_count+? WHERE id=?;

-- name: PendingMessageEditCommands :many
SELECT * FROM message_edit_commands WHERE state='queued' AND next_attempt_at<=? ORDER BY next_attempt_at,id LIMIT 32;

-- name: InterruptedMessageEditCommands :many
SELECT * FROM message_edit_commands WHERE state='dispatching' ORDER BY id;

-- name: FirstContentRevisionSource :one
SELECT r.entity_id FROM message_revisions r WHERE r.id=?
AND r.rowid=(SELECT MIN(first.rowid) FROM message_revisions first WHERE first.entity_id=r.entity_id);

-- name: WitnessedEditContentSource :one
SELECT CAST(MIN(witness.id) AS TEXT) AS source_id
FROM message_edit_commands command
JOIN operations receipt ON receipt.id=command.id AND receipt.resource_id=command.id AND receipt.kind='edit_message'
JOIN messages witness ON witness.id<>command.id AND witness.is_own=1 AND witness.state='submitted'
  AND witness.account_id=command.account_id AND witness.conversation_id=command.conversation_id
JOIN messages root ON root.id=command.message_id AND root.provider_id=root.id AND root.is_own=1
  AND root.account_id=command.account_id AND root.conversation_id=command.conversation_id
LEFT JOIN message_canonical_aliases alias ON alias.alias_id=witness.id
JOIN message_correction_facts fact ON fact.message_id=witness.id AND fact.remote_id=command.id
  AND fact.scope=json_extract(command.payload,'$.Plan.Scope') AND fact.author=json_extract(command.payload,'$.Plan.Author')
  AND fact.target_id=root.provider_id AND fact.target_id=json_extract(command.payload,'$.Plan.OriginalWireID')
JOIN message_revisions content ON content.id=fact.source_revision_id AND content.entity_id=witness.id
LEFT JOIN message_replies reply ON reply.message_id=witness.id AND reply.body_hash=fact.source_hash
WHERE command.state IN ('submitted','failed') AND command.message_id=sqlc.arg(message_id)
  AND command.account_id=sqlc.arg(account_id) AND command.conversation_id=sqlc.arg(conversation_id)
  AND (witness.provider_id=command.id OR (instr(fact.scope,'xmpp:correct:room:')=1
    AND witness.provider_id IN (oie_room_correction_provider_id(fact.author,fact.remote_id),
                                oie_scoped_room_correction_provider_id(fact.scope,fact.author,fact.remote_id))))
  AND (alias.alias_id IS NULL OR alias.canonical_id=command.message_id)
  AND json_extract(command.payload,'$.ContentRevisionID')=CAST(sqlc.arg(content_revision_id) AS TEXT)
  AND json_extract(receipt.result,'$.ID')=command.id AND json_extract(receipt.result,'$.MessageID')=command.message_id
  AND json_extract(receipt.result,'$.AccountID')=command.account_id AND json_extract(receipt.result,'$.ConversationID')=command.conversation_id
  AND json_extract(receipt.result,'$.ExpectedRevisionID')=json_extract(command.payload,'$.Command.ExpectedRevisionID')
  AND json_extract(receipt.result,'$.OriginalText')=json_extract(command.payload,'$.Command.OriginalText')
  AND json_extract(receipt.result,'$.CreatedAt')=json_extract(command.payload,'$.Command.CreatedAt')
  AND fact.source_hash=oie_edit_wire_hash(command.payload)
  AND oie_edit_wire_matches(command.payload,witness.payload,reply.payload)=1
  AND oie_edit_wire_matches(command.payload,content.payload,reply.payload)=1
  AND fact.source_hash=COALESCE(NULLIF(json_extract(witness.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(witness.payload,'$.OriginalText')))
  AND fact.source_hash=COALESCE(NULLIF(json_extract(content.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(content.payload,'$.OriginalText')))
GROUP BY command.message_id HAVING COUNT(DISTINCT witness.id)=1;
