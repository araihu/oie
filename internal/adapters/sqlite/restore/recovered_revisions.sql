INSERT INTO recovered_send_revisions
SELECT r.* FROM backup.recovered_send_revisions r JOIN restore_pending_candidates p ON p.message_id=r.message_id
JOIN recovered_sends s ON s.message_id=p.message_id AND s.source_engine_id=p.source_engine_id
  AND s.conversation_id=p.conversation_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
