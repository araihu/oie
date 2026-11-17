SELECT p.message_id,p.conversation_id,p.source_engine_id,p.revision_id,p.original_state,p.attempt_count,
       p.scheduled_at,p.payload,p.created_at_ms
FROM restore_pending_candidates p
JOIN recovered_sends s ON s.message_id=p.message_id
WHERE s.revision_id<>p.revision_id
   OR s.attempt_count<p.attempt_count
   OR s.original_state<>p.original_state
   OR s.source_engine_id<>p.source_engine_id OR s.conversation_id<>p.conversation_id;
