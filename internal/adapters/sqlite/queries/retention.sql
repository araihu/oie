-- name: GetGlobalRetention :one
SELECT rooms_days,direct_days,revision FROM global_retention WHERE singleton=1;

-- name: PutGlobalRetention :exec
UPDATE global_retention SET rooms_days=?,direct_days=?,revision=revision+1 WHERE singleton=1;

-- name: GetConversationRetention :one
SELECT effective.days AS effective_days,effective.global_days,local.days,COALESCE(local.revision,0) AS revision
FROM effective_retention effective LEFT JOIN conversation_retention local ON local.conversation_id=effective.conversation_id
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

-- name: ResetAccountDiscoveryCheckpoints :exec
DELETE FROM archive_checkpoints WHERE EXISTS (
 SELECT 1 FROM accounts a WHERE a.id=archive_checkpoints.account_id AND a.address=archive_checkpoints.scope
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
SELECT EXISTS (
 SELECT 1 FROM expired_message_references expired
 JOIN conversations c ON c.id=expired.conversation_id
 WHERE c.account_id=sqlc.arg(account_id) AND c.address=sqlc.arg(address)
 AND expired.scope=sqlc.arg(scope) AND expired.remote_id=sqlc.arg(remote_id)
 AND expired.author=sqlc.arg(author) AND expired.age_ms<sqlc.arg(cutoff_ms)
 AND NOT EXISTS (
  SELECT 1 FROM message_correction_facts fact JOIN messages m ON m.id=fact.message_id
  WHERE m.conversation_id=c.id AND fact.scope=expired.scope AND fact.remote_id=expired.remote_id AND fact.author=expired.author
 )
);
