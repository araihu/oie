-- name: PendingMessagesByAttachments :many
SELECT sqlc.embed(m), o.next_attempt_at FROM outbox o JOIN messages m ON o.message_id=m.id
WHERE o.state='queued' AND o.next_attempt_at<=sqlc.arg(next_attempt_at)
AND (COALESCE(json_array_length(m.payload, '$.Attachments'), 0)>0)=CAST(sqlc.arg(with_attachments) AS INTEGER)
AND (o.next_attempt_at,o.message_id) > (sqlc.arg(after_time),sqlc.arg(after_id))
ORDER BY o.next_attempt_at,o.message_id LIMIT sqlc.arg(page_limit);

-- name: MarkOutboxWireStarted :exec
UPDATE outbox SET wire_stage='started' WHERE message_id=? AND wire_stage='awaiting';
