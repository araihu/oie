-- name: InsertMessageSenderIdentity :execrows
INSERT INTO message_sender_identities(message_id,account_id,conversation_id,personal_address,source_hash)
SELECT m.id,m.account_id,m.conversation_id,sqlc.arg(personal_address),sqlc.arg(source_hash)
FROM messages m
WHERE m.id=sqlc.arg(message_id) AND m.account_id=sqlc.arg(account_id)
AND m.conversation_id=sqlc.arg(conversation_id)
AND (SELECT count(*) FROM message_sender_identities i WHERE i.message_id=m.id)<2
ON CONFLICT DO NOTHING;

-- name: ListCanonicalSenderIdentities :many
SELECT i.personal_address,i.source_hash,m.payload AS message_payload
FROM message_sender_identities i JOIN messages m ON m.id=i.message_id
WHERE i.account_id=sqlc.arg(account_id) AND i.conversation_id=sqlc.arg(conversation_id)
AND m.account_id=i.account_id AND m.conversation_id=i.conversation_id
AND (i.message_id=sqlc.arg(canonical_id) OR i.message_id IN
 (SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=sqlc.arg(canonical_id)))
ORDER BY i.message_id,i.personal_address,i.source_hash LIMIT 129;
