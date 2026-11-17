-- A suppressed echo may prove the correction root for an existing native
-- message. Attach only the fact verified by both original source revisions.
INSERT INTO message_correction_facts(message_id,scope,author,remote_id,target_id,source_hash,source_revision_id)
SELECT e.retained_id,e.scope,e.author,e.remote_id,e.target_id,e.source_hash,e.source_revision_id
FROM restore_suppressed_echo_corrections e
JOIN restore_represented_echoes echo ON echo.source_id=e.source_id AND echo.retained_id=e.retained_id
WHERE echo.retained_hash=e.source_hash
ON CONFLICT DO NOTHING
RETURNING message_id,scope,author,remote_id,target_id;
