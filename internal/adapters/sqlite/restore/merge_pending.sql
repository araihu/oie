DELETE FROM restore_pending_candidates
WHERE EXISTS(SELECT 1 FROM messages m WHERE m.id=restore_pending_candidates.message_id)
  OR EXISTS(SELECT 1 FROM expired_message_ids m WHERE m.id=restore_pending_candidates.message_id)
  OR EXISTS(SELECT 1 FROM discarded_recovered_sends m WHERE m.message_id=restore_pending_candidates.message_id)
  OR EXISTS(
      SELECT 1 FROM conversations c JOIN accounts a ON a.id=c.account_id
      JOIN message_retractions r ON r.account_id=c.account_id AND r.kind='direct' AND r.address=c.address
      AND r.scope='xmpp:correct:sender:'||a.address AND r.remote_id=restore_pending_candidates.message_id
      WHERE c.id=restore_pending_candidates.conversation_id AND json_extract(c.payload,'$.Kind')='direct'
  )
  OR EXISTS(
      SELECT 1 FROM messages d JOIN conversations c ON c.id=restore_pending_candidates.conversation_id
      WHERE d.account_id=c.account_id AND d.conversation_id=c.id AND d.is_own=1
        AND d.provider_id=restore_pending_candidates.message_id
  )
  OR EXISTS(
      SELECT 1 FROM operations d WHERE d.resource_id=restore_pending_candidates.message_id
        AND NOT EXISTS(SELECT 1 FROM recovered_send_operations h WHERE h.operation_id=d.id)
  );

INSERT INTO recovered_sends SELECT * FROM restore_pending_candidates
WHERE TRUE ON CONFLICT(message_id) DO NOTHING;

INSERT INTO recovered_send_revisions
SELECT r.id,r.entity_id,r.parents,r.payload,r.created_at
FROM backup.message_revisions r JOIN restore_pending_candidates p ON p.message_id=r.entity_id
JOIN recovered_sends s ON s.message_id=p.message_id AND s.source_engine_id=p.source_engine_id
  AND s.conversation_id=p.conversation_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
