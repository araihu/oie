-- name: GetGlobalRetention :one
SELECT rooms_days,direct_days,revision FROM global_retention WHERE singleton=1;

-- name: PutGlobalRetention :exec
UPDATE global_retention SET rooms_days=?,direct_days=?,revision=revision+1 WHERE singleton=1;

-- name: GetConversationRetention :one
SELECT effective.days AS effective_days,effective.global_days,local.days,CAST(COALESCE(local.revision,0)+g.revision AS INTEGER) AS revision
FROM effective_retention effective CROSS JOIN global_retention g
LEFT JOIN conversation_retention local ON local.conversation_id=effective.conversation_id
WHERE effective.conversation_id=?;

-- name: PutConversationRetention :exec
INSERT INTO conversation_retention(conversation_id,days,revision) VALUES(?,?,1)
ON CONFLICT(conversation_id) DO UPDATE SET days=excluded.days,revision=conversation_retention.revision+1;

-- name: FindEffectiveRetentionDays :one
SELECT days FROM effective_retention WHERE account_id=? AND address=? AND kind=?;

-- name: ListFiniteRetentionConversations :many
SELECT conversation_id,days,kind FROM effective_retention WHERE days>0 AND conversation_id>? ORDER BY conversation_id LIMIT 128;

-- name: ListDirectRetentionPeers :many
SELECT conversation_id,address,days FROM effective_retention WHERE account_id=? AND kind='direct' ORDER BY conversation_id;

-- name: SetMessageSubmittedAt :exec
UPDATE messages SET submitted_at_ms=? WHERE id=? AND submitted_at_ms IS NULL;

-- name: ExpiryCandidates :many
SELECT m.id FROM messages m
WHERE m.conversation_id=sqlc.arg(conversation_id)
  AND COALESCE(m.submitted_at_ms,m.timeline_at_ms,m.created_at_ms)<CAST(sqlc.arg(cutoff_ms) AS INTEGER)
  AND m.id NOT IN (
    SELECT recent.id FROM messages recent WHERE recent.conversation_id=sqlc.arg(conversation_id)
    AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=recent.id)
    AND NOT EXISTS(SELECT 1 FROM hidden_scheduled_messages h WHERE h.message_id=recent.id)
    ORDER BY COALESCE(recent.submitted_at_ms,recent.timeline_at_ms,recent.created_at_ms) DESC,recent.id DESC
    LIMIT sqlc.arg(minimum_messages)
  )
  AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=m.id)
  AND NOT EXISTS(
    SELECT 1 FROM (
     SELECT m.id AS member_id UNION ALL
     SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=m.id
    ) AS members
    WHERE EXISTS(SELECT 1 FROM message_stars s WHERE s.message_id=members.member_id AND s.starred=1)
       OR EXISTS(SELECT 1 FROM outbox o WHERE o.message_id=members.member_id AND o.state IN ('queued','dispatching','unknown'))
       OR EXISTS(SELECT 1 FROM messages reply JOIN outbox o ON o.message_id=reply.id
           WHERE o.state='dispatching' AND reply.conversation_id=m.conversation_id
           AND json_extract(reply.payload,'$.OutboundReply.TargetID')=members.member_id)
       OR EXISTS(SELECT 1 FROM message_edit_commands e WHERE e.message_id=members.member_id AND e.state IN ('queued','dispatching','unknown'))
       OR EXISTS(SELECT 1 FROM message_reaction_commands r WHERE r.message_id=members.member_id AND r.state IN ('queued','dispatching','unknown'))
  )
ORDER BY COALESCE(m.submitted_at_ms,m.timeline_at_ms,m.created_at_ms),m.id
LIMIT sqlc.arg(batch_limit);

-- name: ExpirySourceIDs :many
SELECT alias_id FROM message_canonical_aliases
WHERE canonical_id IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT)));

-- name: DeleteExpiredMessages :exec
DELETE FROM messages WHERE id IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT)));

-- name: DropExpiredMessageEvents :exec
DELETE FROM changes WHERE
 (kind IN ('message.created','message.updated') AND json_extract(payload,'$.ID') IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT))))
 OR (kind IN ('message.preview_changed','message.star_changed') AND json_extract(payload,'$.MessageID') IN (SELECT value FROM json_each(CAST(sqlc.arg(ids) AS TEXT))));

-- name: ScrubConversationEventPreviews :exec
UPDATE changes SET payload=json_set(payload,'$.LastMessage','')
WHERE kind IN ('conversation.created','conversation.updated') AND json_extract(payload,'$.ID')=?
AND json_extract(payload,'$.LastMessage')<>'';

-- name: ScrubConversationRevisionPreviews :exec
UPDATE conversation_revisions SET payload=json_set(payload,'$.LastMessage','')
WHERE entity_id=? AND json_extract(payload,'$.LastMessage')<>'';

-- name: ScrubConversationReceiptPreviews :exec
UPDATE operations SET result=json_set(result,'$.LastMessage','')
WHERE resource_id=? AND json_extract(result,'$.LastMessage')<>'';

-- name: MarkAcceptedDirectHistory :exec
INSERT OR IGNORE INTO accepted_direct_history(conversation_id) VALUES(?);

-- name: HasAcceptedDirectHistory :one
SELECT EXISTS(SELECT 1 FROM accepted_direct_history WHERE conversation_id=?);

-- name: IsExpiredMessage :one
SELECT EXISTS(SELECT 1 FROM expired_message_ids WHERE id=?);

-- name: IsExpiredReplyReference :one
SELECT EXISTS(SELECT 1 FROM expired_message_references
WHERE conversation_id=sqlc.arg(conversation_id) AND remote_id=sqlc.arg(remote_id)
AND (CAST(sqlc.arg(scope) AS TEXT)='' OR scope=CAST(sqlc.arg(scope) AS TEXT))
AND (CAST(sqlc.arg(author) AS TEXT)='' OR author=CAST(sqlc.arg(author) AS TEXT)));

-- name: IsDiscardedReplyReference :one
SELECT EXISTS(SELECT 1 FROM discarded_message_references d JOIN effective_retention c
ON c.account_id=d.account_id AND c.address=d.address AND c.kind=d.kind
WHERE c.conversation_id=sqlc.arg(owner_id) AND d.remote_id=sqlc.arg(remote_id)
AND (CAST(sqlc.arg(scope) AS TEXT)='' OR d.scope=CAST(sqlc.arg(scope) AS TEXT))
AND (CAST(sqlc.arg(author) AS TEXT)='' OR d.author=CAST(sqlc.arg(author) AS TEXT)));

-- name: ResetAccountDiscoveryCheckpoints :exec
DELETE FROM archive_checkpoints WHERE EXISTS (
 SELECT 1 FROM accounts a WHERE a.id=archive_checkpoints.account_id AND (a.address=archive_checkpoints.scope OR 'omemo:'||a.address=archive_checkpoints.scope)
);

-- name: ListRetentionAccountScopes :many
SELECT id FROM accounts ORDER BY id;

-- name: ClearPeerRetentionCheckpoints :exec
DELETE FROM archive_checkpoints WHERE account_id=sqlc.arg(account_id)
AND substr(scope,1,15)='retention-peer:'
AND substr(scope,-length(CAST(sqlc.arg(peer_address) AS TEXT))-1)=':'||CAST(sqlc.arg(peer_address) AS TEXT);

-- name: ClearAllDirectRetentionCheckpoints :exec
DELETE FROM archive_checkpoints WHERE substr(scope,1,15)='retention-peer:';

-- name: IsExpiredCorrectionTarget :one
SELECT EXISTS(SELECT 1 WHERE (
 EXISTS(
  SELECT 1 FROM expired_message_references e JOIN conversations c ON c.id=e.conversation_id
  WHERE c.account_id=sqlc.arg(account_id) AND c.address=sqlc.arg(address)
  AND e.scope=sqlc.arg(scope) AND e.remote_id=sqlc.arg(remote_id) AND e.author=sqlc.arg(author) AND e.age_ms<sqlc.arg(cutoff_ms)
 ) OR EXISTS(
  SELECT 1 FROM discarded_message_references d
  WHERE d.account_id=sqlc.arg(account_id) AND d.address=sqlc.arg(address) AND d.kind=sqlc.arg(kind)
  AND d.scope=sqlc.arg(scope) AND d.remote_id=sqlc.arg(remote_id) AND d.author=sqlc.arg(author) AND d.age_ms<sqlc.arg(cutoff_ms)
 )
) AND NOT EXISTS (
 SELECT 1 FROM message_correction_facts fact JOIN messages m ON m.id=fact.message_id JOIN conversations c ON c.id=m.conversation_id
 WHERE c.account_id=sqlc.arg(account_id) AND c.address=sqlc.arg(address)
 AND fact.scope=sqlc.arg(scope) AND fact.remote_id=sqlc.arg(remote_id) AND fact.author=sqlc.arg(author)
));

-- name: GetArchiveRetentionRevision :one
SELECT CAST(g.archive_revision+COALESCE(a.revision,0) AS INTEGER) FROM global_retention g
LEFT JOIN account_archive_retention a ON a.account_id=sqlc.arg(account_id) WHERE g.singleton=1;

-- name: BumpArchiveRetentionRevision :exec
UPDATE global_retention SET archive_revision=archive_revision+1 WHERE singleton=1;

-- name: BumpAccountArchiveRetentionRevision :exec
INSERT INTO account_archive_retention(account_id,revision) VALUES(?,1)
ON CONFLICT(account_id) DO UPDATE SET revision=revision+1;

-- name: ListInheritedRoomArchiveProgress :many
SELECT a.conversation_id,a.progress FROM room_archives a
JOIN effective_retention effective ON effective.conversation_id=a.conversation_id
LEFT JOIN conversation_retention local ON local.conversation_id=a.conversation_id
WHERE effective.kind='room' AND local.days IS NULL;

-- name: RecordDiscardedMessageReference :execrows
INSERT OR IGNORE INTO discarded_message_references(account_id,address,kind,scope,remote_id,author,age_ms)
VALUES(?,?,?,?,?,?,?);

-- name: ScrubMessageOutboundQuotes :exec
UPDATE messages SET payload=oie_strip_expired_outbound_quote(payload)
WHERE conversation_id=sqlc.arg(conversation_id)
AND json_extract(payload,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ScrubRevisionOutboundQuotes :exec
UPDATE message_revisions SET payload=oie_strip_expired_outbound_quote(payload)
WHERE entity_id IN (SELECT id FROM messages WHERE conversation_id=sqlc.arg(owner_id))
AND json_extract(payload,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ScrubEventOutboundQuotes :exec
UPDATE changes SET payload=oie_strip_expired_outbound_quote(payload)
WHERE kind IN ('message.created','message.updated')
AND json_extract(payload,'$.ConversationID')=sqlc.arg(owner_id)
AND json_extract(payload,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ScrubReceiptOutboundQuotes :exec
UPDATE operations SET result=oie_strip_expired_outbound_quote(result)
WHERE resource_id IN (SELECT id FROM messages WHERE conversation_id=sqlc.arg(owner_id))
AND json_extract(result,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ScrubContentHeadOutboundQuotes :exec
UPDATE message_content_heads SET payload=oie_strip_expired_outbound_quote(payload)
WHERE canonical_id IN (SELECT id FROM messages WHERE conversation_id=sqlc.arg(owner_id))
AND json_extract(payload,'$.OutboundReply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ScrubMessageEditOutboundQuotes :exec
UPDATE message_edit_commands SET payload=oie_strip_expired_edit_quote(payload)
WHERE conversation_id=sqlc.arg(owner_id)
AND json_extract(payload,'$.Reply.TargetID') IN (SELECT value FROM json_each(CAST(sqlc.arg(expired_ids) AS TEXT)));

-- name: ListInboundQuotesInConversation :many
SELECT r.message_id,CAST(COALESCE(a.canonical_id,r.message_id) AS TEXT) AS canonical_id,r.body_hash,r.payload
FROM message_replies r JOIN messages m ON m.id=r.message_id
LEFT JOIN message_canonical_aliases a ON a.alias_id=r.message_id
WHERE m.conversation_id=sqlc.arg(owner_id)
AND json_array_length(json_extract(r.payload,'$.Fallback'))>0;

-- name: ScrubInboundQuoteMetadata :exec
UPDATE message_replies SET payload=oie_scrub_inbound_fallback(payload)
WHERE message_id=sqlc.arg(source_id) AND body_hash=sqlc.arg(body_hash);

-- name: ScrubMessageInboundQuote :exec
UPDATE messages SET payload=oie_strip_expired_inbound_quote(payload,sqlc.arg(reply_metadata))
WHERE id=sqlc.arg(source_id) AND oie_source_hash(json_extract(payload,'$.OriginalText'))=sqlc.arg(body_hash);

-- name: ScrubRevisionInboundQuote :exec
UPDATE message_revisions SET payload=oie_strip_expired_inbound_quote(payload,sqlc.arg(reply_metadata))
WHERE entity_id=sqlc.arg(source_id) AND oie_source_hash(json_extract(payload,'$.OriginalText'))=sqlc.arg(body_hash);

-- name: ScrubEventInboundQuote :exec
UPDATE changes SET payload=oie_strip_expired_inbound_quote(payload,sqlc.arg(reply_metadata))
WHERE kind IN ('message.created','message.updated')
AND json_extract(payload,'$.ID') IN (CAST(sqlc.arg(source_id) AS TEXT),CAST(sqlc.arg(canonical_id) AS TEXT))
AND oie_source_hash(json_extract(payload,'$.OriginalText'))=sqlc.arg(body_hash);

-- name: ScrubContentHeadInboundQuote :exec
UPDATE message_content_heads SET payload=oie_strip_expired_inbound_quote(payload,sqlc.arg(reply_metadata))
WHERE canonical_id=sqlc.arg(canonical_id) AND source_id=sqlc.arg(source_id)
AND oie_source_hash(json_extract(payload,'$.OriginalText'))=sqlc.arg(body_hash);
