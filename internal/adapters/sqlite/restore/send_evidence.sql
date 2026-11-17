-- The send state is evidence even if its history has expired at the destination.
CREATE TEMP TABLE restore_send_evidence (id TEXT PRIMARY KEY,state TEXT NOT NULL,at TEXT, payload TEXT) WITHOUT ROWID;
INSERT INTO restore_send_evidence
SELECT m.id,CASE WHEN COALESCE(o.state,m.state) IN ('cancelled','submitted','failed')
         THEN COALESCE(o.state,m.state) ELSE 'unknown' END,
    (SELECT r.created_at FROM backup.message_revisions r WHERE r.id=m.revision_id AND r.entity_id=m.id),m.payload
FROM backup.messages m LEFT JOIN backup.outbox o ON o.message_id=m.id
JOIN restore_chats c ON c.id=m.conversation_id
LEFT JOIN recovered_sends p ON p.message_id=m.id AND p.conversation_id=c.id
LEFT JOIN restore_messages selected ON selected.id=m.id
LEFT JOIN messages d ON d.id=m.id AND d.conversation_id=m.conversation_id AND d.account_id=m.account_id AND d.is_own=1
WHERE m.is_own=1 AND COALESCE(o.state,m.state) IN ('cancelled','submitted','failed','dispatching','unknown')
  AND (p.message_id IS NOT NULL OR d.state='queued'
       OR (d.id IS NULL AND selected.id IS NULL AND COALESCE(o.state,m.state) IN ('cancelled','submitted','failed')));

-- Another engine can retain our verified own echo under a different local
-- row ID. The scoped sender ID is still the UUID of the original send.
INSERT INTO restore_send_evidence
SELECT b.provider_id,'submitted',r.created_at,b.payload
FROM backup.messages b
JOIN restore_chats x ON x.id=b.conversation_id
JOIN backup.message_revisions r ON r.id=b.revision_id AND r.entity_id=b.id
WHERE b.is_own=1 AND b.state='submitted' AND b.id<>b.provider_id AND (
    EXISTS(SELECT 1 FROM messages m JOIN outbox o ON o.message_id=m.id
           WHERE m.id=b.provider_id AND m.provider_id=m.id AND m.is_own=1
             AND m.account_id=b.account_id AND m.conversation_id=b.conversation_id
             AND m.state='queued' AND o.state='queued')
    OR EXISTS(SELECT 1 FROM recovered_sends p
              WHERE p.message_id=b.provider_id AND p.conversation_id=b.conversation_id)
)
ON CONFLICT(id) DO NOTHING;

INSERT INTO restore_send_evidence
SELECT m.id,'unknown',r.retracted_at,NULL FROM messages m
JOIN restore_chats x ON x.id=m.conversation_id
JOIN conversations c ON c.id=m.conversation_id
JOIN accounts a ON a.id=c.account_id
JOIN message_retractions r ON r.account_id=m.account_id AND r.address=c.address AND r.kind='direct'
  AND r.scope='xmpp:correct:sender:'||a.address AND r.remote_id=m.id
WHERE m.is_own=1 AND m.state='queued' AND json_extract(c.payload,'$.Kind')='direct'
ON CONFLICT(id) DO NOTHING;

-- Expiry implies that the snapshot no longer had an executable send.
INSERT INTO restore_send_evidence
SELECT e.id,'unknown',NULL,NULL FROM backup.expired_message_ids e JOIN messages m ON m.id=e.id
JOIN restore_chats c ON c.id=m.conversation_id
WHERE m.is_own=1 AND m.state='queued'
ON CONFLICT(id) DO NOTHING;

-- Once terminal evidence is available a stale next restore must not recreate
-- a review item, even when retention excluded the terminal message itself.
INSERT INTO discarded_recovered_sends
SELECT e.id FROM restore_send_evidence e
WHERE e.state IN ('cancelled','submitted','failed')
  AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=e.id)
ON CONFLICT(message_id) DO NOTHING;

DELETE FROM recovered_sends
WHERE EXISTS(SELECT 1 FROM discarded_recovered_sends d WHERE d.message_id=recovered_sends.message_id)
   OR EXISTS(SELECT 1 FROM expired_message_ids d WHERE d.id=recovered_sends.message_id);
